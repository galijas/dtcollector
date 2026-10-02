package server

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"dtcollector/internal/report"
)

// Diagnostics (SwarmDialer 1.6.0+ reports). See
// ~/claude/SwarmDialer_report_diagnostics.md for the fields.

var sipNames = map[int]string{
	400: "Bad Request", 401: "Unauthorized", 403: "Forbidden", 404: "Not Found", 407: "Proxy Authentication Required",
	408: "Request Timeout", 480: "Temporarily Unavailable", 481: "Call/Transaction Does Not Exist", 482: "Loop Detected",
	484: "Address Incomplete", 486: "Busy Here", 487: "Request Terminated", 488: "Not Acceptable Here",
	500: "Server Internal Error", 501: "Not Implemented", 502: "Bad Gateway", 503: "Service Unavailable",
	504: "Server Time-out", 600: "Busy Everywhere", 603: "Decline", 604: "Does Not Exist Anywhere", 606: "Not Acceptable",
}

func sipText(code int) string {
	if n, ok := sipNames[code]; ok {
		return fmt.Sprintf("%d (%s)", code, n)
	}
	return fmt.Sprint(code)
}

var causeLabels = map[string]string{
	"rejected": "Rejected by PBXware", "no_response": "No response", "no_answer": "Not answered",
	"setup_error": "Setup error", "dropped": "Dropped by PBXware", "no_audio": "No audio",
}

var causeHelp = map[string]string{
	"rejected":    "PBXware answered the INVITE with an error response",
	"no_response": "no response at all to the INVITE before the dial timeout",
	"no_answer":   "PBXware responded (Trying/Ringing) but never answered",
	"setup_error": "a protocol or local error while setting the call up",
	"dropped":     "answered, then hung up by PBXware before its planned end (counted as answered)",
	"no_audio":    "answered, but SwarmDialer received no audio (counted as answered)",
}

func causeLabel(c string) string {
	if l, ok := causeLabels[c]; ok {
		return l
	}
	return c
}

// failureText is failurePhrase with the pre-1.6.2 caveat and after-stop note.
func failureText(r *report.Report, f report.Failure) string {
	s := failurePhrase(f)
	if failureUnreliable(r, f) {
		s += ", " + unreliableNote
	}
	if n := afterStop(r, f); n > 0 {
		if n >= f.Count {
			s += ", all after the test stopped, from calls still being set up"
		} else {
			s += fmt.Sprintf(" (%d of these failed after the test stopped, from calls still being set up)", n)
		}
	}
	return s
}

// failurePhrase: "37 calls rejected with 503 (Service Unavailable)".
func failurePhrase(f report.Failure) string {
	n, were := fmt.Sprintf("%d call", f.Count), "was"
	if f.Count != 1 {
		n, were = n+"s", "were"
	}
	switch f.Cause {
	case "rejected":
		if f.SIPCode > 0 {
			return n + " rejected with " + sipText(f.SIPCode)
		}
		return n + " rejected"
	case "no_response":
		return n + " got no response"
	case "no_answer":
		return n + " " + were + " not answered"
	case "setup_error":
		return n + " failed with a setup error"
	case "dropped":
		return n + " " + were + " hung up early by PBXware"
	case "no_audio":
		return n + " received no audio"
	}
	return n + ": " + f.Cause
}

func fmtClock(sec int) string {
	if sec < 0 {
		sec = 0
	}
	return fmt.Sprintf("%d:%02d", sec/60, sec%60)
}

// loadRow is one line of the "At stop" table.
type loadRow struct {
	Label, Value, Limit string
	Near                bool // within 5 points of its stop limit
}

func pctText(v float64) string { return fmtNum(v) + "%" }

func atStopRows(a *report.AtStop) []loadRow {
	if a == nil {
		return nil
	}
	row := func(label string, v float64, limit float64) loadRow {
		r := loadRow{Label: label, Value: pctText(v)}
		if limit > 0 {
			r.Limit = pctText(limit)
			r.Near = v >= limit-5
		}
		return r
	}
	rows := []loadRow{{Label: "Calls running at once", Value: fmt.Sprint(a.Calls)},
		row("Host CPU", a.HostCPUPct, 95), row("Host memory", a.HostMemPct, 95), row("Host I/O wait", a.HostIOWaitPct, 0),
		row("SwarmDialer CPU (own cores)", a.SwarmDialerCPUPct, 80)}
	if a.RAMDiskEstPct != nil {
		rows = append(rows, row("Recording RAM disk (estimated)", *a.RAMDiskEstPct, 100))
	}
	for _, role := range roles(a.VPS) {
		v := a.VPS[role]
		if v.CPUPctOfLimit != nil {
			rows = append(rows, row(role+" VPS CPU (of its limit)", *v.CPUPctOfLimit, 95))
		}
		rows = append(rows, row(role+" VPS CPU (of the host)", v.CPUPctOfHost, 0))
		if v.MemPctOfLimit != nil {
			rows = append(rows, row(role+" VPS memory (of its limit)", *v.MemPctOfLimit, 95))
		}
		rows = append(rows, loadRow{Label: role + " VPS memory used", Value: report.HumanBytes(int64(v.MemBytes), 1024)},
			loadRow{Label: role + " Asterisk CPU (of one core)", Value: pctText(v.AsteriskCPUPct)})
		if v.PBXwareActiveCalls != nil {
			rows = append(rows, loadRow{Label: role + " active calls (PBXware's count)", Value: fmtNum(*v.PBXwareActiveCalls)})
		}
	}
	return rows
}

// roles orders MT, CC first, then the rest alphabetically.
func roles[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	rank := map[string]int{"MT": 0, "CC": 1, "swarmdialer": 2}
	sort.Slice(out, func(i, j int) bool {
		ri, ok := rank[out[i]]
		if !ok {
			ri = 9
		}
		rj, ok := rank[out[j]]
		if !ok {
			rj = 9
		}
		if ri != rj {
			return ri < rj
		}
		return out[i] < out[j]
	})
	return out
}

// headroom lists the at-stop loads that have a limit, as "host CPU 61%".
func headroom(a *report.AtStop) []string {
	if a == nil {
		return nil
	}
	out := []string{"host CPU " + pctText(a.HostCPUPct), "host memory " + pctText(a.HostMemPct)}
	for _, role := range roles(a.VPS) {
		v := a.VPS[role]
		if v.CPUPctOfLimit != nil {
			out = append(out, role+" VPS CPU "+pctText(*v.CPUPctOfLimit)+" of its limit")
		}
		if v.MemPctOfLimit != nil {
			out = append(out, role+" VPS memory "+pctText(*v.MemPctOfLimit)+" of its limit")
		}
	}
	return append(out, "SwarmDialer CPU "+pctText(a.SwarmDialerCPUPct))
}

// Attribution (DTCollector_attribution_fix.md): SwarmDialer runs on the host
// it tests, so its VPS drops outgoing packets when the host is saturated.
// Drops then are a side effect of the host's load, not a SwarmDialer limit.

const (
	busyHostCPU     = 80.0 // host CPU % at which the host counts as busy
	significantDrop = 0.1  // drop % from which drops affect the figures
	toolLimitDrop   = 1.0  // drop % that, on a quiet host, limits the result
	toolCPULimit    = 80.0 // SwarmDialer CPU stop limit
)

// sendDropPct is the share of SwarmDialer's outgoing packets its VPS
// dropped: the 1.6.1 field, else estimated from the call load (2 legs x 50
// packets/s per call). ok is false when nothing was dropped or is unknown.
func sendDropPct(t report.Test) (pct float64, ok bool) {
	tool := t.Result.Tool
	if tool == nil || tool.UDPSendErrors == 0 {
		return 0, false
	}
	if tool.UDPSendDropPct != nil {
		return *tool.UDPSendDropPct, true
	}
	iv := t.Timeseries.IntervalS
	if iv <= 0 {
		iv = 5
	}
	sent := 0.0
	for _, c := range series(t, "concurrent_calls") {
		if c != nil {
			sent += *c * 2 * 50 * iv
		}
	}
	if sent <= 0 {
		return 0, false
	}
	return 100 * float64(tool.UDPSendErrors) / (sent + float64(tool.UDPSendErrors)), true
}

// hostCPUAtDrops is the host CPU when the drops began: the 1.6.1 event's
// value (known), else the test's peak host CPU (an estimate).
func hostCPUAtDrops(t report.Test) (cpu float64, known bool) {
	for _, e := range t.Result.Events {
		if e.Code == "udp_send_drops" && e.Value != nil {
			return *e.Value, true
		}
	}
	peak := 0.0
	for _, v := range series(t, "host_cpu_pct") {
		if v != nil && *v > peak {
			peak = *v
		}
	}
	return peak, false
}

// attribution sums up who limited a test.
type attribution struct {
	dropPct      float64
	drops        bool // drops_significant
	hostBusy     bool // host_busy_at_drops
	hostCPU      float64
	hostCPUKnown bool // from the 1.6.1 event; else hostCPU is the test's peak
	toolLimited  bool // swarmdialer_limited
	mediaSuspect bool // MOS, audio received and loss are unreliable
}

func attribute(t report.Test) attribution {
	var a attribution
	pct, ok := sendDropPct(t)
	a.dropPct = pct
	a.drops = ok && pct >= significantDrop
	if ok {
		a.hostCPU, a.hostCPUKnown = hostCPUAtDrops(t)
		a.hostBusy = a.hostCPU >= busyHostCPU
	}
	res := t.Result
	// Only an overload stop or SwarmDialer's CPU at its limit blame
	// SwarmDialer. Send drops alone never do: with CPU to spare, its VPS
	// dropping packets is the host not moving them out in time.
	a.toolLimited = res.StopReason == "swarmdialer_overloaded" ||
		(res.Tool != nil && res.Tool.SwarmDialerCPUPeakPct >= toolCPULimit)
	a.mediaSuspect = a.drops
	return a
}

func (a attribution) dropText() string {
	when := "the report doesn't record the host's load when the drops began"
	if a.hostCPUKnown {
		when = "host CPU " + pct1(a.hostCPU) + " when the drops began"
	}
	tail := "SwarmDialer runs on the tested host and had CPU to spare, so the host didn't move its packets out in time. "
	if a.toolLimited {
		tail = "SwarmDialer runs on the tested host. "
	}
	return fmt.Sprintf("SwarmDialer's VPS dropped %s of its outgoing packets (%s). %s"+
		"This affects the audio figures (MOS, audio received, loss), not the call failures.", pct1(a.dropPct), when, tail)
}

// Reports before SwarmDialer 1.6.2 misclassified call timeouts: a call
// PBXware didn't answer within 15 s was often reported as rejected with 401
// (a late auth challenge) or 487 (the answer to SwarmDialer's own CANCEL),
// a cause first seen at 0 calls appeared only after the test stopped, and
// failed_calls has a false spike at the first cooldown sample.
const failureFixVersion = "1.6.2"

func legacyFailures(r *report.Report) bool {
	return r.Source == nil && r.HasDiagnostics() && !report.VersionAtLeast(r.SwarmDialerVersion, failureFixVersion)
}

// failureUnreliable: a cause the pre-1.6.2 classification can't be trusted for.
func failureUnreliable(r *report.Report, f report.Failure) bool {
	return legacyFailures(r) && f.Cause == "rejected" && (f.SIPCode == 401 || f.SIPCode == 487)
}

const unreliableNote = "probably not answered within 15 s (classification unreliable before SwarmDialer 1.6.2)"

// failureFirstAt is when the cause first appeared while calls were placed;
// ok is false when it only appeared after the test stopped.
func failureFirstAt(r *report.Report, f report.Failure) (calls int, ok bool) {
	if f.FirstAtCalls == nil || (legacyFailures(r) && *f.FirstAtCalls == 0) || (f.AfterStop > 0 && f.AfterStop >= f.Count) {
		return 0, false
	}
	return *f.FirstAtCalls, true
}

// afterStop is how many of a cause's failures came after the test stopped.
func afterStop(r *report.Report, f report.Failure) int {
	if f.AfterStop > 0 {
		return f.AfterStop
	}
	if legacyFailures(r) && f.FirstAtCalls != nil && *f.FirstAtCalls == 0 {
		return f.Count
	}
	return 0
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// pct1 formats a percentage with at most one decimal: "29.1%", "2.4%", "40%".
func pct1(v float64) string { return strconv.FormatFloat(round1(v), 'f', -1, 64) + "%" }

// mediaSuspect: the test's MOS, audio received and loss are unreliable.
func mediaSuspect(r *report.Report, t report.Test) bool {
	return r.HasDiagnostics() && attribute(t).mediaSuspect
}

// toolWarnings are the warnings about the load generator.
func toolWarnings(test report.Test) []string {
	t := test.Result.Tool
	if t == nil {
		return nil
	}
	var w []string
	if t.UDPSendErrors > 0 {
		if a := attribute(test); a.drops {
			w = append(w, a.dropText())
		} else {
			w = append(w, fmt.Sprintf("SwarmDialer's VPS dropped %d outgoing packets (under 0.1%% of what it sent); this doesn't affect the figures.", t.UDPSendErrors))
		}
	}
	if t.UDPReceiveErrors > 0 {
		w = append(w, fmt.Sprintf("SwarmDialer dropped %d incoming packets (receive buffer full or errors).", t.UDPReceiveErrors))
	}
	for _, role := range roles(t.MediaReceived) {
		if m := t.MediaReceived[role]; m.LossPct > 1 {
			w = append(w, fmt.Sprintf("%s of the audio from %s was lost between PBXware and SwarmDialer.", pct1(m.LossPct), role))
		}
	}
	if t.NotRegisteredAtEnd > 0 {
		w = append(w, fmt.Sprintf("%d of %d extensions weren't registered at the end, so they couldn't take calls.", t.NotRegisteredAtEnd, t.Extensions))
	}
	if t.SwarmDialerCPUPeakPct >= toolCPULimit {
		w = append(w, fmt.Sprintf("SwarmDialer's CPU peaked at %s, at or over its 80%% limit.", pctText(t.SwarmDialerCPUPeakPct)))
	}
	return w
}

// pbxwareNotes compares PBXware's own counts with SwarmDialer's.
func pbxwareNotes(res report.Result) []string {
	p := res.PBXware
	if p == nil {
		return nil
	}
	var out []string
	for _, role := range roles(p.ActiveCallsPeak) {
		peak := p.ActiveCallsPeak[role]
		diff := peak - float64(res.MaxConcurrentCalls)
		if math.Abs(diff) >= math.Max(2, 0.01*float64(res.MaxConcurrentCalls)) {
			what := "call legs were lost"
			if diff > 0 {
				what = "channels were left hanging"
			}
			out = append(out, fmt.Sprintf("PBXware counted at most %s active calls on %s, SwarmDialer %d: %s.", fmtNum(peak), role, res.MaxConcurrentCalls, what))
		}
	}
	for _, role := range roles(p.CDRStatus) {
		var other []string
		for _, st := range roles(p.CDRStatus[role]) {
			if n := p.CDRStatus[role][st]; st != "Answered" && n > 0 {
				other = append(other, fmt.Sprintf("%d %s", n, st))
			}
		}
		if len(other) > 0 {
			out = append(out, fmt.Sprintf("%s CDRs: %s.", role, strings.Join(other, ", ")))
		}
		if a, ok := p.CDRStatus[role]["Answered"]; ok && a != res.Calls.Answered {
			out = append(out, fmt.Sprintf("%s CDRs show %d answered calls, SwarmDialer %d.", role, a, res.Calls.Answered))
		}
	}
	return out
}

// diagInfo explains how a diagnostics test ended, in the spec's order:
// tool, the stop, failures, PBXware's view, quality.
func diagInfo(r *report.Report, t report.Test) string {
	res := t.Result
	var out []string
	a := attribute(t)
	if a.toolLimited {
		out = append(out, "The result is limited by SwarmDialer, the load generator, not by the host.")
	}
	if res.StopDetail != "" {
		s := "Stopped: " + res.StopDetail + "."
		if h := headroom(res.AtStop); len(h) > 0 && res.StopReason != "target_reached" {
			s += " Load at that point: " + strings.Join(h, ", ") + "."
		}
		out = append(out, s)
	}
	if len(res.Failures) > 0 {
		var parts []string
		first := -1
		for i, f := range res.Failures {
			if i < 3 {
				parts = append(parts, failureText(r, f))
			}
			if c, ok := failureFirstAt(r, f); ok && (first < 0 || c < first) {
				first = c
			}
		}
		s := ""
		if first >= 0 {
			s = fmt.Sprintf("Failures began at %d calls: ", first)
		} else {
			s = "Failures: "
		}
		out = append(out, s+strings.Join(parts, "; ")+".")
	}
	out = append(out, pbxwareNotes(res)...)
	if res.QualityDegradedAtCalls != nil {
		s := fmt.Sprintf("Call quality degraded from %d calls", *res.QualityDegradedAtCalls)
		if res.QualityDegradedReason != "" {
			s += " (" + res.QualityDegradedReason + ")"
		}
		out = append(out, s+".")
	}
	if a.drops && !a.toolLimited {
		out = append(out, a.dropText())
	}
	for _, w := range toolWarnings(t) {
		if !strings.HasPrefix(w, "SwarmDialer's VPS dropped") {
			out = append(out, w)
		}
	}
	return strings.Join(out, " ")
}

// eventText describes one timeline event.
func eventText(e report.Event) string {
	val := ""
	if e.Value != nil {
		val = fmtNum(*e.Value)
	}
	metric := map[string]string{"host_cpu": "Host CPU", "host_mem": "Host memory", "vps_cpu": "VPS CPU (of its limit)",
		"vps_mem": "VPS memory (of its limit)", "swarmdialer_cpu": "SwarmDialer CPU"}
	switch e.Code {
	case "threshold_near":
		m := metric[e.Metric]
		if m == "" {
			m = e.Metric
		}
		if e.Role != "" {
			m = e.Role + " " + m
		}
		return fmt.Sprintf("%s passed 80%% of its stop threshold (%s%%)", m, val)
	case "quality_degraded":
		if e.Metric == "setup_p95_ms" {
			return "Call quality dropped: p95 call setup " + val + " ms"
		}
		return "Call quality dropped: " + val + "% of new calls failed"
	case "failures_started":
		s := "Failures started: " + strings.ToLower(causeLabel(e.Cause))
		if e.SIPCode > 0 {
			s += " (" + sipText(e.SIPCode) + ")"
		}
		return s
	case "ramdisk_full_estimated":
		return "Recording RAM disk estimated full (" + val + " MB); recordings are cut off from here"
	case "registration_lost":
		return fmt.Sprintf("%d more extensions failed to re-register", e.Count)
	case "api_error":
		src := map[string]string{"prometheus_host": "host metrics (Prometheus)", "prometheus_vps": "VPS metrics (Prometheus)",
			"pbxware_cdr": "PBXware CDRs", "pbxware_mos": "PBXware MOS"}[e.Reason]
		if src == "" {
			src = e.Reason
		}
		return "Reading " + src + " failed; data from it may be missing"
	case "stop":
		return "Test stopped placing calls: " + stopLabel(e.Reason)
	case "udp_send_drops":
		s := fmt.Sprintf("SwarmDialer's VPS started dropping outgoing packets (%d in 5 s)", e.Count)
		if e.Value != nil {
			s += fmt.Sprintf(", host CPU %s", pct1(*e.Value))
		}
		return s
	}
	return e.Code
}

// testInfo is the (i) explanation of a test: diagnostics when the report
// has them, otherwise the one derived from the older report data.
func testInfo(r *report.Report, t report.Test) string {
	if r.HasDiagnostics() {
		return diagInfo(r, t)
	}
	return stopInfo(r, t)
}

// diagCtx is what the "diagnostics" template gets: the report and one test.
type diagCtx struct {
	R *report.Report
	T report.Test
}

// mainFailure is compare's "Main failure cause" cell and its (i) note.
func mainFailure(r *report.Report, f report.Failure) (text, info string) {
	text = causeLabel(f.Cause)
	if f.SIPCode > 0 {
		text += " " + sipText(f.SIPCode)
	}
	if c, ok := failureFirstAt(r, f); ok {
		text += fmt.Sprintf(", from %d calls", c)
	} else {
		text += ", after the stop"
	}
	text += fmt.Sprintf(" (%d)", f.Count)
	if failureUnreliable(r, f) {
		text += "*"
		info = "Probably not answered within 15 s: SwarmDialer before 1.6.2 misclassified call timeouts as 401 or 487."
	}
	return text, info
}
