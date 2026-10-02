package server

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"dtcollector/internal/report"
	"dtcollector/internal/store"
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
	if rollingReached(r, t) {
		c := rollingCap(t)
		return fmt.Sprintf("%d calls ran at once. At its highest dial rate (%s calls/s with %s s calls) this rolling test can run at most %d calls at once, "+
			"so its real target is %d, not %d. Reports before SwarmDialer 1.6.0 judged it against %d and marked it \"Target not reached\"; "+
			"by the 1.6.0 rule (%d or more, 99%% of %d) the target was reached.",
			res.MaxConcurrentCalls, strconv.FormatFloat(t.DialRateCPS, 'f', -1, 64), strconv.FormatFloat(t.CallDurationS, 'f', -1, 64),
			c, c, testTarget(r), testTarget(r), int(math.Ceil(rollingReachedShare*float64(c))), c)
	}
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

// Rolling tests: SwarmDialer 1.6.0 set their target to what the highest
// rate can run (rate x call length, 510 in the standard profile) and counts
// 99% of it as reached. Older reports were judged against 512, which the
// rolling test can't reach, so their rolling results are re-judged here.
const rollingReachedShare = 0.99

func rollingCap(t report.Test) int {
	if t.Mode != "rolling" || t.DialRateCPS <= 0 || t.CallDurationS <= 0 {
		return 0
	}
	return int(t.DialRateCPS * t.CallDurationS)
}

// rollingReached: an older report's rolling test that the 1.6.0 rule counts
// as having reached its target.
func rollingReached(r *report.Report, t report.Test) bool {
	if r.HasDiagnostics() || t.Result.StopReason != "target_not_reached" {
		return false
	}
	c := rollingCap(t)
	return c > 0 && c < testTarget(r) && float64(t.Result.MaxConcurrentCalls) >= math.Ceil(rollingReachedShare*float64(c))
}

// effectiveStop is the stop reason to show: the reported one, except older
// rolling tests re-judged by the 1.6.0 rule.
func effectiveStop(r *report.Report, t report.Test) string {
	if rollingReached(r, t) {
		return "target_reached"
	}
	return t.Result.StopReason
}

// listStop does the same for the report list, which only has the test's ID,
// calls and stop reason: the standard profile's rolling test reaches 505 of
// its 510 calls or more.
func listStop(row store.ReportRow, tr report.TestResult) string {
	if !row.HasDiagnostics() && tr.StopReason == "target_not_reached" && strings.HasPrefix(tr.ID, "rolling") &&
		tr.MaxConcurrentCalls >= int(math.Ceil(rollingReachedShare*510)) && tr.MaxConcurrentCalls <= 510 {
		return "target_reached"
	}
	return tr.StopReason
}
