package server

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"dtcollector/internal/report"
)

const defaultTarget = 512

// testTarget is the concurrent-call target: the PBXware license channels
// (the standard profile tests up to the license), else 512.
func testTarget(r *report.Report) int {
	t := 0
	for _, p := range r.Environment.PBXware {
		if p.LicenseChannels > 0 && (t == 0 || p.LicenseChannels < t) {
			t = p.LicenseChannels
		}
	}
	if t == 0 {
		return defaultTarget
	}
	return t
}

func series(t report.Test, key string) []*float64 {
	var v []*float64
	if raw, ok := t.Timeseries.Series[key]; ok {
		json.Unmarshal(raw, &v)
	}
	return v
}

func pctOf(n, total int) string {
	if total == 0 {
		return "0%"
	}
	p := 100 * float64(n) / float64(total)
	if p < 1 && n > 0 {
		return fmt.Sprintf("%.1f%%", p)
	}
	return fmt.Sprintf("%.0f%%", p)
}

// stopInfo explains a "target not reached" result from the report's own data
// (call counts, time series, quality marker); empty for other stop reasons.
func stopInfo(r *report.Report, t report.Test) string {
	res := t.Result
	if res.StopReason != "target_not_reached" {
		return ""
	}
	target := testTarget(r)
	var out []string
	out = append(out, fmt.Sprintf("%d of %d calls ran at once.", res.MaxConcurrentCalls, target))

	designCap := 0
	if t.Mode == "rolling" && t.DialRateCPS > 0 && t.CallDurationS > 0 {
		designCap = int(t.DialRateCPS * t.CallDurationS)
		if designCap < target {
			out = append(out, fmt.Sprintf("At the highest dial rate (%s calls/s with %s s calls), at most %d calls can run at once, so this test can't reach %d by design.",
				strconv.FormatFloat(t.DialRateCPS, 'f', -1, 64), strconv.FormatFloat(t.CallDurationS, 'f', -1, 64), designCap, target))
		}
	}

	c := res.Calls
	if c.Failed > 0 {
		s := fmt.Sprintf("%d of %d calls failed (%s).", c.Failed, c.Started, pctOf(c.Failed, c.Started))
		failed, conc := series(t, "failed_calls"), series(t, "concurrent_calls")
		for i, f := range failed {
			if f != nil && *f > 0 && i < len(conc) && conc[i] != nil {
				s += fmt.Sprintf(" Failures started at %d concurrent calls.", int(*conc[i]))
				break
			}
		}
		out = append(out, s)
	} else if designCap == 0 || designCap >= target {
		out = append(out, "No calls failed: every call was answered, but earlier calls ended before the last ones started, so they never all ran at the same time.")
	}

	if q := res.QualityDegradedAtCalls; q != nil {
		out = append(out, fmt.Sprintf("Call quality degraded from %d concurrent calls (more than 1%% of new calls failing, or p95 call setup over 500 ms).", *q))
	}
	peak := 0.0
	for _, v := range series(t, "host_cpu_pct") {
		if v != nil && *v > peak {
			peak = *v
		}
	}
	if peak > 0 {
		out = append(out, fmt.Sprintf("Host CPU peaked at %s%%.", fmtNum(peak)))
	}
	return strings.Join(out, " ")
}
