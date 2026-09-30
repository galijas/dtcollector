package server

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"

	"dtcollector/internal/hardware"
	"dtcollector/internal/store"
)

// hwFilter mirrors the HW Validation page's filters, so the PDF holds the
// rows the page shows.
type hwFilter struct {
	Query, Category, Source, Status string
}

var (
	searchTrademarkRe = regexp.MustCompile(`\((r|tm)\)|[®™]`)
	searchNonWordRe   = regexp.MustCompile(`[^a-z0-9]+`)
)

// searchNorm is the page's norm(): lower case, trademarks and punctuation
// ignored, so "E5-2699", "E5 2699" and "e52699" find the same part.
func searchNorm(s string) string {
	s = searchTrademarkRe.ReplaceAllString(strings.ToLower(s), " ")
	return strings.TrimSpace(searchNonWordRe.ReplaceAllString(s, " "))
}

func label(list []struct{ ID, Label string }, id string) string {
	for _, x := range list {
		if x.ID == id {
			return x.Label
		}
	}
	return id
}

func attrText(a hardware.Attrs, key string) string {
	switch key {
	case "speed":
		return a.Speed
	case "driver":
		return a.Driver
	case "type":
		return a.Type
	case "ports":
		var s []string
		for _, p := range a.Ports {
			s = append(s, fmt.Sprint(p))
		}
		return strings.Join(s, ", ")
	}
	return ""
}

func (f hwFilter) match(p store.HWPart) bool {
	if (f.Category != "" && p.Category != f.Category) || (f.Source != "" && p.Source != f.Source) ||
		(f.Status != "" && p.Status != f.Status) {
		return false
	}
	words := strings.Fields(searchNorm(f.Query))
	if len(words) == 0 {
		return true
	}
	text := append([]string{p.Name, p.Comment, label(hardware.Statuses, p.Status), label(hardware.Sources, p.Source),
		p.CreatedBy, attrText(p.Attrs, "speed"), attrText(p.Attrs, "ports"), attrText(p.Attrs, "driver"),
		attrText(p.Attrs, "type")}, p.Aliases...)
	hay := " " + searchNorm(strings.Join(text, " ")) + " "
	compact := strings.ReplaceAll(hay, " ", "")
	for _, w := range words {
		if !strings.Contains(hay, w) && !strings.Contains(compact, w) {
			return false
		}
	}
	return true
}

func (f hwFilter) describe() string {
	var parts []string
	if q := strings.TrimSpace(f.Query); q != "" {
		parts = append(parts, fmt.Sprintf("search %q", q))
	}
	for _, c := range hardware.Categories {
		if c.ID == f.Category {
			parts = append(parts, "type "+c.Label)
		}
	}
	if f.Source != "" {
		parts = append(parts, "source "+label(hardware.Sources, f.Source))
	}
	if f.Status != "" {
		parts = append(parts, "status "+label(hardware.Statuses, f.Status))
	}
	return strings.Join(parts, ", ")
}

// sourceText spells out the source for paper, where there is no hover text.
func sourceText(p store.HWPart) string {
	l := label(hardware.Sources, p.Source)
	switch p.Source {
	case hardware.SourceManual:
		if p.CreatedBy != "" {
			return l + " (" + p.CreatedBy + ")"
		}
	case hardware.SourceTestScript, hardware.SourceSWHW:
		if p.SourceReportID != "" {
			return l + " (report " + p.SourceReportID[:8] + ")"
		}
		return l + " (report deleted)"
	}
	return l
}

type pdfCol struct {
	title string
	width float64
	value func(store.HWPart) string
	bold  bool
}

func pdfColumns(c hardware.Category) []pdfCol {
	const page = 277.0 // A4 landscape minus 10 mm margins
	cols := []pdfCol{{title: "Name", width: 68, value: func(p store.HWPart) string { return p.Name }, bold: true}}
	for _, x := range c.Columns {
		key := x.Key
		w := 20.0
		if key == "ports" {
			w = 16
		}
		cols = append(cols, pdfCol{title: x.Label, width: w, value: func(p store.HWPart) string { return attrText(p.Attrs, key) }})
	}
	cols = append(cols,
		pdfCol{title: "Status", width: 23, value: func(p store.HWPart) string { return label(hardware.Statuses, p.Status) }},
		pdfCol{title: "Source", width: 47, value: sourceText})
	used := 0.0
	for _, col := range cols {
		used += col.width
	}
	return append(cols, pdfCol{title: "Comment", width: page - used, value: func(p store.HWPart) string { return p.Comment }})
}

var statusRGB = map[string][3]int{
	hardware.StatusSupported: {19, 135, 90}, hardware.StatusUnsupported: {212, 47, 74}, hardware.StatusUnverified: {88, 97, 116},
}

func hardwarePDF(parts []store.HWPart, f hwFilter, by string, total int) *fpdf.Fpdf {
	pdf := fpdf.New("L", "mm", "A4", "")
	tr := pdf.UnicodeTranslatorFromDescriptor("")
	pdf.SetMargins(10, 10, 10)
	pdf.SetAutoPageBreak(false, 10)
	pdf.AliasNbPages("")
	generated := time.Now().UTC().Format("2006-01-02 15:04 UTC")
	pdf.SetTitle("DT Collector HW Validation list", true)
	pdf.SetCreator("DT Collector", true)
	pdf.SetFooterFunc(func() {
		pdf.SetY(-8)
		pdf.SetFont("Helvetica", "", 7)
		pdf.SetTextColor(88, 97, 116)
		pdf.CellFormat(0, 4, tr("DT Collector HW Validation list, exported "+generated), "", 0, "L", false, 0, "")
		pdf.CellFormat(0, 4, fmt.Sprintf("Page %d of {nb}", pdf.PageNo()), "", 0, "R", false, 0, "")
	})
	pdf.AddPage()
	pdf.SetFont("Courier", "B", 18)
	pdf.SetTextColor(21, 25, 34)
	pdf.CellFormat(0, 9, "HW Validation list", "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 9)
	pdf.SetTextColor(88, 97, 116)
	summary := fmt.Sprintf("Exported %s by %s. %d of %d parts.", generated, by, len(parts), total)
	if d := f.describe(); d != "" {
		summary += " Filters: " + d + "."
	}
	pdf.CellFormat(0, 5, tr(summary), "", 1, "L", false, 0, "")
	pdf.Ln(3)

	const lh, pad, bottom = 3.6, 0.8, 196.0
	if len(parts) == 0 {
		pdf.SetFont("Helvetica", "", 10)
		pdf.CellFormat(0, 8, "No hardware matches the filters.", "", 1, "L", false, 0, "")
		return pdf
	}
	header := func(cols []pdfCol) {
		pdf.SetFont("Helvetica", "B", 8)
		pdf.SetFillColor(241, 243, 246)
		pdf.SetTextColor(88, 97, 116)
		pdf.SetDrawColor(213, 218, 227)
		for _, c := range cols {
			pdf.CellFormat(c.width, 6, tr(c.title), "1", 0, "L", true, 0, "")
		}
		pdf.Ln(-1)
	}
	for _, c := range hardware.Categories {
		var rows []store.HWPart
		for _, p := range parts {
			if p.Category == c.ID {
				rows = append(rows, p)
			}
		}
		if len(rows) == 0 {
			continue
		}
		cols := pdfColumns(c)
		if pdf.GetY() > bottom-20 {
			pdf.AddPage()
		}
		pdf.Ln(2)
		pdf.SetFont("Courier", "B", 12)
		pdf.SetTextColor(21, 25, 34)
		pdf.CellFormat(0, 7, tr(fmt.Sprintf("%s (%d)", c.Label, len(rows))), "", 1, "L", false, 0, "")
		header(cols)
		for _, p := range rows {
			lines := make([][]string, len(cols))
			n := 1
			for i, col := range cols {
				style := ""
				if col.bold {
					style = "B"
				}
				pdf.SetFont("Helvetica", style, 8)
				text := tr(col.value(p))
				if text == "" {
					text = "-"
				}
				lines[i] = pdf.SplitText(text, col.width-2*pad)
				if len(lines[i]) > n {
					n = len(lines[i])
				}
			}
			h := float64(n)*lh + 2*pad
			if pdf.GetY()+h > bottom {
				pdf.AddPage()
				header(cols)
			}
			x, y := pdf.GetX(), pdf.GetY()
			for i, col := range cols {
				pdf.SetDrawColor(213, 218, 227)
				pdf.Rect(x, y, col.width, h, "D")
				style := ""
				if col.bold {
					style = "B"
				}
				pdf.SetFont("Helvetica", style, 8)
				pdf.SetTextColor(21, 25, 34)
				if col.title == "Status" {
					rgb := statusRGB[p.Status]
					pdf.SetTextColor(rgb[0], rgb[1], rgb[2])
				} else if col.title == "Comment" || col.title == "Source" {
					pdf.SetTextColor(88, 97, 116)
				}
				for j, line := range lines[i] {
					pdf.SetXY(x+pad, y+pad+float64(j)*lh)
					pdf.CellFormat(col.width-2*pad, lh, line, "", 0, "L", false, 0, "")
				}
				x += col.width
			}
			pdf.SetXY(10, y+h)
		}
	}
	return pdf
}

func (s *Server) handleHardwarePDF(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := hwFilter{Query: q.Get("q"), Category: q.Get("type"), Source: q.Get("source"), Status: q.Get("status")}
	all, err := s.st.ListParts()
	if err != nil {
		s.internalError(w, r, "list hardware", err)
		return
	}
	var parts []store.HWPart
	for _, p := range all {
		if f.match(p) {
			parts = append(parts, p)
		}
	}
	pdf := hardwarePDF(parts, f, adminFrom(r).Username, len(all))
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="dtcollector-hw-validation-%s.pdf"`,
		time.Now().UTC().Format("2006-01-02")))
	if err := pdf.Output(w); err != nil {
		s.log.Printf("hardware pdf: %v", err)
	}
}
