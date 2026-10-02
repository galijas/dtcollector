package server

import (
	"fmt"
	"math"
	"sort"
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

// toolWarnings are the spec's warnings about the load generator.
func toolWarnings(t *report.ToolHealth) []string {
	if t == nil {
		return nil
	}
	var w []string
	if t.UDPSendErrors > 0 {
		w = append(w, fmt.Sprintf("SwarmDialer dropped %d outgoing packets (send queue full), so packet loss seen by PBXware may be the tool's fault.", t.UDPSendErrors))
	}
	if t.UDPReceiveErrors > 0 {
		w = append(w, fmt.Sprintf("SwarmDialer dropped %d incoming packets (receive buffer full or errors).", t.UDPReceiveErrors))
	}
	for _, role := range roles(t.MediaReceived) {
		if m := t.MediaReceived[role]; m.LossPct > 1 {
			w = append(w, fmt.Sprintf("%s%% of the audio from %s was lost between PBXware and SwarmDialer.", fmtNum(m.LossPct), role))
		}
	}
	if t.NotRegisteredAtEnd > 0 {
		w = append(w, fmt.Sprintf("%d of %d extensions weren't registered at the end, so they couldn't take calls.", t.NotRegisteredAtEnd, t.Extensions))
	}
	if t.SwarmDialerCPUPeakPct >= 80 {
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
func diagInfo(t report.Test) string {
	res := t.Result
	var out []string
	tw := toolWarnings(res.Tool)
	if res.StopReason == "swarmdialer_overloaded" || (res.Tool != nil && res.Tool.UDPSendErrors > 0) {
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
				parts = append(parts, failurePhrase(f))
			}
			if f.FirstAtCalls != nil && (first < 0 || *f.FirstAtCalls < first) {
				first = *f.FirstAtCalls
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
	out = append(out, tw...)
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
	}
	return e.Code
}

// testInfo is the (i) explanation of a test: diagnostics when the report
// has them, otherwise the one derived from the older report data.
func testInfo(r *report.Report, t report.Test) string {
	if r.HasDiagnostics() {
		return diagInfo(t)
	}
	return stopInfo(r, t)
}
