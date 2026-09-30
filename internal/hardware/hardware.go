// Package hardware defines the HW Validation list: hardware parts by
// category, how names are normalized for matching, how parts are extracted
// from uploaded reports, and the datasheet seed imported at install.
package hardware

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

const (
	CatServer     = "server_model"
	CatCPU        = "cpu"
	CatNIC        = "nic"
	CatDrive      = "drive"
	CatController = "storage_controller"
)

const (
	StatusSupported   = "supported"
	StatusUnsupported = "unsupported"
	StatusUnverified  = "unverified"
)

const (
	SourceDatasheet  = "datasheet"
	SourceManual     = "manual"
	SourceTestScript = "test_script"
	SourceSWHW       = "swhw"
)

// Column is a category-specific attribute shown in the list and the form.
type Column struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

type Category struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Columns []Column `json:"columns"`
}

// Only attributes that uploaded reports can fill are columns; the datasheet's
// other details (chipset, media, interface, capacities...) go into the comment.
var Categories = []Category{
	{ID: CatServer, Label: "Server Model"},
	{ID: CatCPU, Label: "CPU"},
	{ID: CatNIC, Label: "NIC", Columns: []Column{{"speed", "Speed"}, {"ports", "Ports seen"}, {"driver", "Linux driver"}}},
	{ID: CatDrive, Label: "Drive (HDD, SSD)", Columns: []Column{{"type", "Type"}}},
	{ID: CatController, Label: "Storage Controller"},
}

var Statuses = []struct{ ID, Label string }{
	{StatusSupported, "Supported"}, {StatusUnsupported, "Unsupported"}, {StatusUnverified, "Not validated"},
}

var Sources = []struct{ ID, Label string }{
	{SourceManual, "Manual input"}, {SourceTestScript, "Test Script"},
	{SourceSWHW, "SWHW data"}, {SourceDatasheet, "Supported Hardware Datasheet"},
}

var DriveTypes = []string{"HDD", "SSD", "NVMe SSD"}

func ValidCategory(c string) bool {
	for _, x := range Categories {
		if x.ID == c {
			return true
		}
	}
	return false
}

func ValidStatus(s string) bool {
	for _, x := range Statuses {
		if x.ID == s {
			return true
		}
	}
	return false
}

type Attrs struct {
	Speed  string `json:"speed,omitempty"`
	Ports  []int  `json:"ports,omitempty"`
	Driver string `json:"driver,omitempty"`
	Type   string `json:"type,omitempty"`
}

type Part struct {
	ID             int64    `json:"id"`
	Category       string   `json:"category"`
	Name           string   `json:"name"`
	Status         string   `json:"status"`
	Comment        string   `json:"comment"`
	Attrs          Attrs    `json:"attrs"`
	Aliases        []string `json:"aliases"`
	Source         string   `json:"source"`
	SourceReportID string   `json:"source_report_id,omitempty"`
	CreatedBy      string   `json:"created_by,omitempty"`
	CreatedAt      string   `json:"created_at"`
	UpdatedBy      string   `json:"updated_by,omitempty"`
	UpdatedAt      string   `json:"updated_at"`
}

var (
	trademarkRe = regexp.MustCompile(`(?i)\((r|tm)\)|[®™]`)
	cpuNoiseRe  = regexp.MustCompile(`(?i)@\s*[\d.]+\s*ghz|\b\d+-core\b|\bcpu\b|\bprocessor\b`)
	spacesRe    = regexp.MustCompile(`\s+`)
	noiseWords  = map[string]bool{"corporation": true, "corp": true, "inc": true, "co": true, "ltd": true, "llc": true,
		"subsidiaries": true, "technologies": true, "technology": true, "company": true}
)

// Key normalizes a name for matching: case, trademarks, company suffixes,
// punctuation and spacing are ignored ("Intel(R) Xeon(R) CPU E5-2699 v4 @
// 2.20GHz" and "Intel Xeon E5-2699 v4" give the same key).
func Key(category, s string) string {
	s = strings.ToLower(trademarkRe.ReplaceAllString(s, " "))
	s = strings.ReplaceAll(s, "and subsidiaries", " ")
	if category == CatCPU {
		s = cpuNoiseRe.ReplaceAllString(s, " ")
	}
	var b strings.Builder
	for _, w := range strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if !noiseWords[w] {
			b.WriteString(w)
		}
	}
	return b.String()
}

// Keys are the part's match keys: its name and every alias.
func (p *Part) Keys() []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range append([]string{p.Name}, p.Aliases...) {
		if k := Key(p.Category, s); k != "" && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

func tidy(s string) string { return strings.TrimSpace(spacesRe.ReplaceAllString(s, " ")) }

// CleanCPU turns a CPU model string into its short name:
// "Intel(R) Xeon(R) Silver 4208 CPU @ 2.10GHz" -> "Intel Xeon Silver 4208".
func CleanCPU(s string) string {
	return tidy(cpuNoiseRe.ReplaceAllString(trademarkRe.ReplaceAllString(s, " "), " "))
}

var (
	vendorSuffixRe = regexp.MustCompile(`(?i)[,\s]+(inc\.?( and subsidiaries)?|corporation|corp\.?|co\.?,?( ltd\.?)?|ltd\.?|llc|technologies|technology|company|computer)$`)
	vendorNames    = map[string]string{
		"super micro": "Supermicro", "supermicro": "Supermicro", "hewlett packard enterprise": "HPE",
		"hewlett-packard": "HP", "hp": "HP", "hpe": "HPE", "dell": "Dell", "gigabyte": "Gigabyte",
		"giga-byte": "Gigabyte", "intel": "Intel", "mellanox": "Mellanox", "broadcom": "Broadcom", "lenovo": "Lenovo",
		"samsung": "Samsung", "micron": "Micron", "qlogic": "QLogic", "emulex": "Emulex",
	}
)

// CleanVendor shortens vendor strings: "Intel Corporation" -> "Intel",
// "Super Micro Computer Inc" -> "Supermicro", "Dell Inc." -> "Dell".
func CleanVendor(s string) string {
	s = tidy(trademarkRe.ReplaceAllString(s, " "))
	for {
		t := strings.TrimSpace(vendorSuffixRe.ReplaceAllString(s, ""))
		if t == s || t == "" {
			break
		}
		s = t
	}
	if v, ok := vendorNames[strings.ToLower(s)]; ok {
		return v
	}
	return s
}

// JoinName prefixes the vendor unless the product already starts with it.
func JoinName(vendor, product string) string {
	vendor, product = tidy(vendor), tidy(product)
	if vendor == "" || strings.HasPrefix(strings.ToLower(product), strings.ToLower(vendor)) {
		return product
	}
	return vendor + " " + product
}

// SpeedLabel formats a link speed in Mbit/s: 10000 -> "10GbE", 2500 -> "2.5GbE".
func SpeedLabel(mbps float64) string {
	if mbps <= 0 {
		return ""
	}
	if mbps < 1000 {
		return fmt.Sprintf("%.0fMbE", mbps)
	}
	return strconv.FormatFloat(mbps/1000, 'f', -1, 64) + "GbE"
}

// DriveType maps a report's disk type (nvme, ssd, hdd, unknown) to the list's.
func DriveType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "nvme":
		return "NVMe SSD"
	case "ssd":
		return "SSD"
	case "hdd":
		return "HDD"
	}
	return ""
}

// ParsePorts reads "2, 4" into [2 4].
func ParsePorts(s string) ([]int, error) {
	var out []int
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		n, err := strconv.Atoi(f)
		if err != nil || n <= 0 || n > 64 {
			return nil, fmt.Errorf("ports must be whole numbers from 1 to 64, separated by commas")
		}
		out = AddPort(out, n)
	}
	return out, nil
}

// AddPort adds n to a sorted port list if it isn't there.
func AddPort(ports []int, n int) []int {
	for i, p := range ports {
		if p == n {
			return ports
		}
		if p > n {
			return append(ports[:i], append([]int{n}, ports[i:]...)...)
		}
	}
	return append(ports, n)
}
