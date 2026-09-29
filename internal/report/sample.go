package report

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"time"
)

// SampleHost describes one synthetic host for Sample.
type SampleHost struct {
	CPUModel string
	Sockets  int
	Cores    int
	Threads  int
	MaxMHz   float64
	MemGiB   int64
	// CPUPerCall is host CPU % used per concurrent call without
	// recording or transcoding; the other tests scale it up.
	CPUPerCall float64
	Disk       Disk
}

var SampleHosts = []SampleHost{
	{"Intel(R) Xeon(R) Gold 6338 CPU @ 2.00GHz", 2, 64, 128, 3200, 256, 0.045, Disk{"SAMSUNG MZQL2960HCJR", 960e9, "nvme"}},
	{"AMD EPYC 7443P 24-Core Processor", 1, 24, 48, 4000, 128, 0.07, Disk{"Micron 5300 MTFD", 1920e9, "ssd"}},
	{"Intel(R) Xeon(R) E-2288G CPU @ 3.70GHz", 1, 8, 16, 5000, 64, 0.17, Disk{"WDC WD4003FRYZ", 4000e9, "hdd"}},
}

type sampleTest struct {
	id, mode, recording, caller, callee string
	costFactor                          float64
}

var standardTests = []sampleTest{
	{"ramp_norec_low", "ramp", "off", "ulaw", "ulaw", 1.0},
	{"ramp_norec_high", "ramp", "off", "opus", "ulaw", 2.4},
	{"ramp_mono_low", "ramp", "mono", "ulaw", "ulaw", 1.4},
	{"ramp_mono_high", "ramp", "mono", "opus", "ulaw", 2.9},
	{"ramp_stereo_low", "ramp", "stereo", "ulaw", "ulaw", 1.6},
	{"ramp_stereo_high", "ramp", "stereo", "opus", "ulaw", 3.2},
	{"rolling_stereo", "rolling", "stereo", "ulaw", "ulaw", 2.0},
}

// Sample builds a complete, valid "standard" v1 report for a synthetic host,
// for tests and local UI development. seed makes it deterministic.
func Sample(h SampleHost, seed uint64, created time.Time) *Report {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	id := fmt.Sprintf("%08x-%04x-4%03x-a%03x-%012x", rng.Uint32(), rng.Uint32()&0xffff,
		rng.Uint32()&0xfff, rng.Uint32()&0xfff, rng.Uint64()&0xffffffffffff)
	vps := VPSLimits{CPULimit: 0, CPUShare: ptr(512.0), MemLimitMB: 8192, CallrecRAMMB: ptr(512.0)}
	r := &Report{
		SchemaVersion:      SchemaVersion,
		ReportID:           id,
		CreatedAt:          created.UTC().Format(time.RFC3339),
		SwarmDialerVersion: "1.4.0",
		Profile:            Profile{"standard", 1},
		Environment: Environment{
			Serverware: Serverware{"5.2.1", "standalone"},
			Host: Host{
				CPUModel: h.CPUModel, CPUSockets: h.Sockets, CPUCores: h.Cores, CPUThreads: h.Threads,
				CPUMaxMHz: h.MaxMHz, MemoryBytes: h.MemGiB << 30,
				Disks:   []Disk{h.Disk, h.Disk},
				Network: []NIC{{10000}, {10000}},
			},
			VPS: map[string]VPSLimits{
				"pbxware_mt":  vps,
				"pbxware_cc":  vps,
				"swarmdialer": {CPULimit: 0, MemLimitMB: 4096},
			},
			PBXware: []PBXware{
				{"MT", "8.2.0.0", "Multi-Tenant", 512},
				{"CC", "8.2.0.0", "Call Centre", 512},
			},
		},
	}
	start := created.Add(-4 * time.Hour).UTC()
	for _, st := range standardTests {
		t := sampleRun(rng, h, st, start)
		r.Tests = append(r.Tests, t)
		fin, _ := time.Parse(time.RFC3339, t.FinishedAt)
		start = fin.Add(30 * time.Second)
	}
	return r
}

func sampleRun(rng *rand.Rand, h SampleHost, st sampleTest, start time.Time) Test {
	const interval = 5.0
	const target = 512
	perCall := h.CPUPerCall * st.costFactor
	rate, callDur := 1.25, 600.0
	if st.mode == "rolling" {
		rate, callDur = 8.5, 60.0
	}
	baseline, hold, cooldown := 60.0, 60.0, 60.0

	var calls, cpu, mem, iow, rx, tx, dr, dw, setup, failed []*float64
	grp := func() map[string][]*float64 {
		return map[string][]*float64{"MT": nil, "CC": nil}
	}
	vcpu := map[string][]*float64{"MT": nil, "CC": nil, "swarmdialer": nil}
	vmem := map[string][]*float64{"MT": nil, "CC": nil, "swarmdialer": nil}
	ast := grp()

	stop := "target_reached"
	maxCalls, cur := 0, 0.0
	phase := "baseline"
	phaseT, holdLeft := 0.0, hold
	var atCPU, atMem float64
	var atAst = map[string]float64{}
	var setupAll []float64
	elapsed := 0.0
	rolStep := 1.0
	for {
		switch phase {
		case "baseline":
			if phaseT >= baseline {
				phase, phaseT = "ramp", 0
			}
		case "ramp":
			r := rate
			if st.mode == "rolling" {
				// dial rate raised step by step: +1 cps every 30 s up to 8.5
				rolStep = math.Min(rate, 1+math.Floor(phaseT/30))
				r = rolStep
				cur = math.Min(target, r*callDur)
				if r >= rate {
					cur = target
				}
			} else {
				cur = math.Min(target, cur+r*interval)
			}
			if cur >= target {
				phase, phaseT = "hold", 0
			}
		case "hold":
			holdLeft -= interval
			if holdLeft <= 0 {
				phase, phaseT = "cooldown", 0
			}
		case "cooldown":
			cur = math.Max(0, cur-target/(cooldown/interval)*1.5)
			if phaseT >= cooldown {
				goto done
			}
		}
		{
			noise := func(a float64) float64 { return a * (1 + (rng.Float64()-0.5)*0.08) }
			c := 3 + cur*perCall
			if c >= 100 && stop == "target_reached" && phase == "ramp" {
				c = 100
				stop = "host_cpu_100"
				phase, phaseT = "cooldown", 0
			}
			c = math.Min(100, noise(c))
			m := 18 + cur*0.02
			if st.recording != "off" {
				m += cur * 0.015
			}
			calls = append(calls, ptr(math.Round(cur)))
			cpu = append(cpu, ptr(round2(c)))
			mem = append(mem, ptr(round2(noise(m))))
			io := 0.3 + cur*0.0015
			if st.recording != "off" {
				io += cur * 0.004
			}
			iow = append(iow, ptr(round2(noise(io))))
			bps := cur * 2 * 87200
			rx = append(rx, ptr(math.Round(noise(bps))))
			tx = append(tx, ptr(math.Round(noise(bps))))
			wr := 20000.0
			if st.recording == "mono" {
				wr += cur * 16000
			} else if st.recording == "stereo" {
				wr += cur * 29000
			}
			dr = append(dr, ptr(math.Round(noise(4000))))
			dw = append(dw, ptr(math.Round(noise(wr))))
			load := c / 100
			s := 40 + 400*math.Pow(load, 6)
			setup = append(setup, ptr(round2(noise(s))))
			if cur > 0 {
				setupAll = append(setupAll, s)
			}
			f := 0.0
			if load > 0.95 {
				f = math.Floor(rng.Float64() * 4)
			}
			failed = append(failed, ptr(f))
			share := cur * perCall
			vcpu["MT"] = append(vcpu["MT"], ptr(round2(noise(1+share*0.55))))
			vcpu["CC"] = append(vcpu["CC"], ptr(round2(noise(1+share*0.40))))
			vcpu["swarmdialer"] = append(vcpu["swarmdialer"], ptr(round2(noise(0.5+share*0.05))))
			vmem["MT"] = append(vmem["MT"], ptr(math.Round(noise(1.2e9+cur*3.1e6))))
			vmem["CC"] = append(vmem["CC"], ptr(math.Round(noise(1.1e9+cur*2.8e6))))
			vmem["swarmdialer"] = append(vmem["swarmdialer"], ptr(math.Round(noise(2e8+cur*1.1e6))))
			ast["MT"] = append(ast["MT"], ptr(round2(noise(0.5+share*0.52*float64(h.Threads)))))
			ast["CC"] = append(ast["CC"], ptr(round2(noise(0.5+share*0.38*float64(h.Threads)))))
			if int(cur) > maxCalls {
				maxCalls = int(cur)
			}
			if phase == "hold" || (stop != "target_reached" && atCPU == 0) {
				atCPU, atMem = round2(c), round2(m)
				atAst["MT"] = *ast["MT"][len(ast["MT"])-1]
				atAst["CC"] = *ast["CC"][len(ast["CC"])-1]
			}
		}
		phaseT += interval
		elapsed += interval
	}
done:
	finished := start.Add(time.Duration(elapsed) * time.Second)
	started := float64(maxCalls)
	if st.mode == "rolling" {
		started = rate * (elapsed - baseline - cooldown)
	}
	failedTotal := 0
	for _, f := range failed {
		failedTotal += int(*f)
	}
	avg, p95, mx := stats(setupAll)
	res := Result{
		StopReason:         stop,
		MaxConcurrentCalls: maxCalls,
		Calls:              Calls{int(started) + failedTotal, int(started), failedTotal},
		SetupMS:            Stats{round2(avg), round2(p95), round2(mx)},
		MOS:                MOS{round2(4.4 - 0.3*math.Pow(atCPU/100, 4)), round2(4.1 - 0.9*math.Pow(atCPU/100, 4))},
		RTPReceivedRatio:   ptr(round4(0.9995 - 0.02*math.Pow(atCPU/100, 6))),
		AtTarget:           AtTarget{atCPU, atMem, atAst},
	}
	if st.recording == "stereo" {
		// 512 MB RAM disk at ~29 KB/s per call
		secs := 512 * 1024.0 / (29 * float64(maxCalls))
		est := start.Add(time.Duration((baseline + 300 + secs) * float64(time.Second))).Format(time.RFC3339)
		atCalls := int(math.Min(float64(maxCalls), 512*1024/(29*callDur)))
		rec := &RecordingResult{RamdiskFullEstimatedAtCalls: &atCalls, RamdiskFullEstimatedAt: &est}
		if st.mode == "rolling" {
			trend := "stable"
			d := 2 + perCall*40
			if perCall*float64(target) > 60 {
				trend = "growing"
				d *= 3
			}
			rec.MP3ConversionDelayS = &MP3Delay{round2(d), round2(d * 1.8), round2(d * 2.6), trend}
		}
		res.Recording = rec
	}
	series := map[string]any{
		"concurrent_calls": calls, "host_cpu_pct": cpu, "host_mem_pct": mem, "host_iowait_pct": iow,
		"host_net_rx_bps": rx, "host_net_tx_bps": tx, "host_disk_read_bps": dr, "host_disk_write_bps": dw,
		"vps_cpu_pct": vcpu, "vps_mem_bytes": vmem, "asterisk_cpu_pct": ast,
		"setup_ms_p95": setup, "failed_calls": failed,
	}
	raw := map[string]json.RawMessage{}
	for k, v := range series {
		b, _ := json.Marshal(v)
		raw[k] = b
	}
	format := "wav49"
	if st.recording == "stereo" {
		format = "wav"
	} else if st.recording == "off" {
		format = ""
	}
	return Test{
		ID: st.id, Mode: st.mode, CallType: "remote",
		Codec:     Codec{st.caller, st.callee},
		Recording: st.recording, RecordingFormat: format,
		CallDurationS: callDur, DialRateCPS: rate,
		StartedAt: start.Format(time.RFC3339), FinishedAt: finished.Format(time.RFC3339),
		Result:     res,
		Timeseries: Timeseries{IntervalS: interval, Start: start.Format(time.RFC3339), Series: raw},
	}
}

func stats(v []float64) (avg, p95, max float64) {
	if len(v) == 0 {
		return
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	sum := 0.0
	for _, x := range s {
		sum += x
	}
	return sum / float64(len(s)), s[int(float64(len(s)-1)*0.95)], s[len(s)-1]
}

func ptr[T any](v T) *T        { return &v }
func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round4(v float64) float64 { return math.Round(v*10000) / 10000 }

// SampleHardware builds a hardware-only report. With script set it looks
// like the hardware collection script's upload (source instead of
// swarmdialer_version, no VPS or PBXware section); otherwise like
// SwarmDialer's "Upload Hardware Info Only".
func SampleHardware(h SampleHost, seed uint64, created time.Time, script bool) *Report {
	r := Sample(h, seed, created)
	r.Profile = Profile{HardwareProfile, 1}
	r.Tests = []Test{}
	r.Environment.Host.SystemVendor = "Supermicro"
	r.Environment.Host.SystemModel = "SYS-6029BT-DNC0R"
	r.Environment.Host.StorageControllers = []Controller{{"Broadcom / LSI", "SAS3008 PCI-Express Fusion-MPT SAS-3", 1}}
	r.Environment.Host.NICs = []NICModel{{"Intel Corporation", "Ethernet Controller X550", "ixgbe", 2, 10000}}
	r.Environment.Host.Bonds = []Bond{{2}}
	r.Environment.Host.Motherboard = &Motherboard{"Supermicro", "X11DPT-B", "1.02", "American Megatrends Inc.", "3.5", "05/15/2021"}
	if script {
		r.SwarmDialerVersion = ""
		r.Source = &Source{"hw-collect", "1.0.0"}
		r.Environment.VPS = map[string]VPSLimits{}
		r.Environment.PBXware = []PBXware{}
	}
	return r
}
