package server

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"dtcollector/internal/report"
	"dtcollector/internal/store"
)

const maxCompare = 8

type compareReport struct {
	Meta *store.ReportRow
	R    *report.Report
}

type compareCell struct {
	Text string
	Info string // explanation behind an (i) button, e.g. why the target wasn't reached
	Best bool
}

type compareRow struct {
	Label string
	Cells []compareCell
}

type compareTest struct {
	ID    string
	Title string
	Rows  []compareRow
}

type compareData struct {
	Reports []compareReport
	Tests   []compareTest
	IDs     []string
}

type metric struct {
	label  string
	num    func(t *report.Test) (float64, bool)
	str    func(t *report.Test) (string, bool)
	format func(float64) string
	better int // +1 higher is better, -1 lower is better, 0 no ranking
}

func some(v float64) (float64, bool) { return v, true }

func compareMetrics(reports []compareReport) []metric {
	ms := []metric{
		{label: "Max concurrent calls", num: func(t *report.Test) (float64, bool) { return some(float64(t.Result.MaxConcurrentCalls)) }, format: fmtNum, better: 1},
		{label: "Stop reason", str: func(t *report.Test) (string, bool) { return stopLabel(t.Result.StopReason), true }},
		{label: "Stop detail", str: func(t *report.Test) (string, bool) { return t.Result.StopDetail, t.Result.StopDetail != "" }},
		{label: "Main failure cause", str: func(t *report.Test) (string, bool) {
			if len(t.Result.Failures) == 0 {
				return "", false
			}
			f := t.Result.Failures[0]
			s := causeLabel(f.Cause)

			if f.SIPCode > 0 {
				s += " " + sipText(f.SIPCode)
			}
			if f.FirstAtCalls != nil {
				s += fmt.Sprintf(", from %d calls", *f.FirstAtCalls)
			}
			return fmt.Sprintf("%s (%d)", s, f.Count), true
		}},
		{label: "Host CPU at target", num: func(t *report.Test) (float64, bool) { return some(t.Result.AtTarget.HostCPUPct) }, format: fmtPct, better: -1},
		{label: "Host RAM at target", num: func(t *report.Test) (float64, bool) { return some(t.Result.AtTarget.HostMemPct) }, format: fmtPct, better: -1},
	}
	groups := map[string]bool{}
	for _, cr := range reports {
		for _, t := range cr.R.Tests {
			for g := range t.Result.AtTarget.AsteriskCPUPct {
				groups[g] = true
			}
		}
	}
	names := make([]string, 0, len(groups))
	for g := range groups {
		names = append(names, g)
	}
	sort.Strings(names)
	for _, g := range names {
		ms = append(ms, metric{label: "Asterisk CPU at target (" + g + ")", num: func(t *report.Test) (float64, bool) {
			v, ok := t.Result.AtTarget.AsteriskCPUPct[g]
			return v, ok
		}, format: fmtPct}) // scales with core count, so lower isn't "better"
	}
	ms = append(ms,
		metric{label: "Call setup avg", num: func(t *report.Test) (float64, bool) { return some(t.Result.SetupMS.Avg) }, format: fmtMS, better: -1},
		metric{label: "Call setup p95", num: func(t *report.Test) (float64, bool) { return some(t.Result.SetupMS.P95) }, format: fmtMS, better: -1},
		metric{label: "Call setup max", num: func(t *report.Test) (float64, bool) { return some(t.Result.SetupMS.Max) }, format: fmtMS, better: -1},
		metric{label: "MOS avg", num: func(t *report.Test) (float64, bool) { return some(t.Result.MOS.Avg) }, format: fmtNum, better: 1},
		metric{label: "MOS min", num: func(t *report.Test) (float64, bool) { return some(t.Result.MOS.Min) }, format: fmtNum, better: 1},
		metric{label: "RTP received", num: func(t *report.Test) (float64, bool) {
			if t.Result.RTPReceivedRatio == nil {
				return 0, false
			}
			return *t.Result.RTPReceivedRatio * 100, true
		}, format: func(v float64) string { return fmt.Sprintf("%.2f%%", v) }, better: 1},
		metric{label: "Failed calls", num: func(t *report.Test) (float64, bool) { return some(float64(t.Result.Calls.Failed)) }, format: fmtNum, better: -1},
		metric{label: "Quality degraded at", num: func(t *report.Test) (float64, bool) {
			if t.Result.QualityDegradedAtCalls == nil {
				return 0, false
			}
			return float64(*t.Result.QualityDegradedAtCalls), true
		}, format: func(v float64) string { return fmtNum(v) + " calls" }, better: 1},
		metric{label: "RAM disk full at (est.)", num: func(t *report.Test) (float64, bool) {
			if t.Result.Recording == nil || t.Result.Recording.RamdiskFullEstimatedAtCalls == nil {
				return 0, false
			}
			return float64(*t.Result.Recording.RamdiskFullEstimatedAtCalls), true
		}, format: func(v float64) string { return fmtNum(v) + " calls" }, better: 1},
	)
	mp3 := func(f func(d *report.MP3Delay) float64) func(t *report.Test) (float64, bool) {
		return func(t *report.Test) (float64, bool) {
			if t.Result.Recording == nil || t.Result.Recording.MP3ConversionDelayS == nil {
				return 0, false
			}
			return f(t.Result.Recording.MP3ConversionDelayS), true
		}
	}
	ms = append(ms,
		metric{label: "MP3 conversion delay avg", num: mp3(func(d *report.MP3Delay) float64 { return d.Avg }), format: fmtSec, better: -1},
		metric{label: "MP3 conversion delay p95", num: mp3(func(d *report.MP3Delay) float64 { return d.P95 }), format: fmtSec, better: -1},
		metric{label: "MP3 conversion delay max", num: mp3(func(d *report.MP3Delay) float64 { return d.Max }), format: fmtSec, better: -1},
		metric{label: "MP3 conversion delay trend", str: func(t *report.Test) (string, bool) {
			if t.Result.Recording == nil || t.Result.Recording.MP3ConversionDelayS == nil {
				return "", false
			}
			return t.Result.Recording.MP3ConversionDelayS.Trend, true
		}},
	)
	return ms
}

func fmtPct(v float64) string { return fmtNum(v) + "%" }
func fmtMS(v float64) string  { return fmtNum(v) + " ms" }
func fmtSec(v float64) string { return fmtNum(v) + " s" }

var mediaRows = map[string]bool{"MOS avg": true, "MOS min": true, "RTP received": true}

func findTest(r *report.Report, id string) *report.Test {
	for i := range r.Tests {
		if r.Tests[i].ID == id {
			return &r.Tests[i]
		}
	}
	return nil
}

func buildCompare(reports []compareReport) []compareTest {
	var order []string
	seen := map[string]bool{}
	titles := map[string]string{}
	for _, cr := range reports {
		for _, t := range cr.R.Tests {
			if !seen[t.ID] {
				seen[t.ID] = true
				order = append(order, t.ID)
				titles[t.ID] = testTitle(t)
			}
		}
	}
	metrics := compareMetrics(reports)
	var out []compareTest
	for _, id := range order {
		ct := compareTest{ID: id, Title: titles[id]}
		tests := make([]*report.Test, len(reports))
		for i, cr := range reports {
			tests[i] = findTest(cr.R, id)
		}
		for _, m := range metrics {
			row := compareRow{Label: m.label, Cells: make([]compareCell, len(reports))}
			any := false
			vals := make([]float64, len(reports))
			has := make([]bool, len(reports))
			for i, t := range tests {
				row.Cells[i].Text = "–"
				if t == nil {
					continue
				}
				if m.str != nil {
					if v, ok := m.str(t); ok && v != "" {
						row.Cells[i].Text, any = v, true
						if m.label == "Main failure cause" {
							row.Cells[i].Text, row.Cells[i].Info = mainFailure(reports[i].R, t.Result.Failures[0])
						}
						if m.label == "Stop reason" {
							row.Cells[i].Text = stopLabel(effectiveStop(reports[i].R, *t))
							row.Cells[i].Info = testInfo(reports[i].R, *t)
						}
					}
					continue
				}
				if v, ok := m.num(t); ok {
					row.Cells[i].Text, vals[i], has[i], any = m.format(v), v, true, true
					// Media figures of a test whose audio SwarmDialer's VPS dropped are
					// marked and left out of the best-value ranking.
					if mediaRows[m.label] && mediaSuspect(reports[i].R, *t) {
						row.Cells[i].Text += "*"
						row.Cells[i].Info = "Unreliable: " + attribute(*t).dropText()
						has[i] = false
					}
				}
			}
			if !any {
				continue
			}
			markBest(row.Cells, vals, has, m.better)
			ct.Rows = append(ct.Rows, row)
		}
		out = append(out, ct)
	}
	return out
}

// markBest flags the best value in a row, only when at least two reports
// have a value and they differ.
func markBest(cells []compareCell, vals []float64, has []bool, better int) {
	if better == 0 {
		return
	}
	n, best, differ := 0, 0.0, false
	for i := range vals {
		if !has[i] {
			continue
		}
		if n == 0 {
			best = vals[i]
		} else {
			if vals[i] != best {
				differ = true
			}
			if (better > 0 && vals[i] > best) || (better < 0 && vals[i] < best) {
				best = vals[i]
			}
		}
		n++
	}
	if n < 2 || !differ {
		return
	}
	for i := range vals {
		if has[i] && vals[i] == best {
			cells[i].Best = true
		}
	}
}

func (s *Server) handleCompare(w http.ResponseWriter, r *http.Request) {
	var ids []string
	seen := map[string]bool{}
	for _, v := range r.URL.Query()["ids"] {
		for _, id := range strings.Split(v, ",") {
			id = strings.ToLower(strings.TrimSpace(id))
			if id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	if len(ids) < 2 {
		s.errorPage(w, r, http.StatusBadRequest, "Select at least two reports to compare.")
		return
	}
	if len(ids) > maxCompare {
		s.errorPage(w, r, http.StatusBadRequest, "Compare at most 8 reports at a time.")
		return
	}
	d := compareData{IDs: ids}
	for _, id := range ids {
		meta, rep, ok := s.loadReport(w, r, id)
		if !ok {
			return
		}
		d.Reports = append(d.Reports, compareReport{meta, rep})
	}
	for _, cr := range d.Reports {
		if cr.Meta.IsHardware() {
			s.errorPage(w, r, http.StatusBadRequest, "Hardware-only reports have no test results, so they can't be compared. Select benchmark reports.")
			return
		}
	}
	// Diagnostics reports (SwarmDialer 1.6.0+) and older ones have different
	// overviews and rolling-test MOS isn't comparable, so they aren't mixed.
	diag0 := d.Reports[0].R.HasDiagnostics()
	for _, cr := range d.Reports[1:] {
		if cr.R.HasDiagnostics() != diag0 {
			s.errorPage(w, r, http.StatusBadRequest, "Diagnostics reports (SwarmDialer 1.6.0 and later) can't be compared with older reports. "+
				"Select reports of one kind: all with the Diagnostics badge, or all without it.")
			return
		}
	}
	p0 := d.Reports[0].Meta
	for _, cr := range d.Reports[1:] {
		if cr.Meta.ProfileName != p0.ProfileName || cr.Meta.ProfileVersion != p0.ProfileVersion {
			s.errorPage(w, r, http.StatusBadRequest, "Reports can only be compared within one test profile version. "+
				"The selection mixes "+p0.Profile()+" and "+cr.Meta.Profile()+".")
			return
		}
	}
	d.Tests = buildCompare(d.Reports)
	s.render(w, r, http.StatusOK, "compare.html", "Compare reports", d)
}
