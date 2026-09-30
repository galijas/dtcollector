package hardware

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
)

// The datasheet lists, generated from the Supported / Not Supported hardware
// sheets (~/claude/Resources/Hardware/build_hardware_lists.py). Imported once,
// when the database is created or upgraded to the HW Validation schema.
//
//go:embed seed/supported-hardware.json seed/unsupported-hardware.json seed/sw-analytics-hardware.json
var seedFS embed.FS

type seedEntry struct {
	Name       string   `json:"name"`
	Vendor     string   `json:"vendor"`
	Model      string   `json:"model"`
	Notes      string   `json:"notes"`
	Reason     string   `json:"reason"`
	Aliases    []string `json:"aliases"`
	Chipset    *string  `json:"chipset"`
	Speed      string   `json:"speed"`
	Media      string   `json:"media"`
	Ports      []int    `json:"ports"`
	Driver     *string  `json:"linux_driver"`
	Type       string   `json:"type"`
	Interface  string   `json:"interface"`
	FormFactor string   `json:"form_factor"`
	Capacities []string `json:"capacities"`
	Count      int      `json:"count"`
}

type seedFile struct {
	ServerModels []seedEntry `json:"server_models"`
	CPUs         []seedEntry `json:"cpus"`
	NICs         []seedEntry `json:"nics"`
	NICs10G      []seedEntry `json:"nics_10g_plus"`
	NICs1G       []seedEntry `json:"nics_1g"`
	Drives       []seedEntry `json:"drives"`
	Controllers  []seedEntry `json:"storage_controllers"`
	Period       struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"period"`
	Removed []struct {
		Type string `json:"type"`
		Name string `json:"name"`
	} `json:"removed"`
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func sentence(label, v string) string {
	if strings.TrimSpace(v) == "" {
		return ""
	}
	return label + ": " + strings.TrimSuffix(v, ".") + "."
}

func joinComment(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}

func convert(cat string, e seedEntry, status, source string) Part {
	p := Part{Category: cat, Name: e.Name, Status: status, Source: source}
	// Aliases with and without the vendor, so report strings like
	// "Supermicro SYS-1029TP-DC0R" match a sheet's "SYS-1029TP-DC0R".
	for _, a := range e.Aliases {
		p.Aliases = append(p.Aliases, a)
		if e.Vendor != "" && !strings.HasPrefix(strings.ToLower(a), strings.ToLower(CleanVendor(e.Vendor))) {
			p.Aliases = append(p.Aliases, CleanVendor(e.Vendor)+" "+a)
		}
	}
	reason := e.Reason
	switch cat {
	case CatNIC:
		p.Attrs = Attrs{Speed: e.Speed, Ports: e.Ports, Driver: deref(e.Driver)}
		chip := deref(e.Chipset)
		if strings.EqualFold(chip, e.Model) {
			chip = ""
		}
		p.Comment = joinComment(reason, sentence("Chipset", chip), sentence("Media", e.Media), e.Notes)
	case CatDrive:
		t := e.Type
		if strings.EqualFold(e.Interface, "NVMe") && strings.EqualFold(t, "SSD") {
			t = "NVMe SSD"
		}
		p.Attrs = Attrs{Type: t}
		p.Comment = joinComment(reason, sentence("Interface", e.Interface), sentence("Form factor", e.FormFactor),
			sentence("Capacities seen", strings.Join(e.Capacities, ", ")), e.Notes)
	case CatController:
		p.Comment = joinComment(reason, sentence("Type", e.Type), sentence("Chipset", deref(e.Chipset)),
			sentence("Linux driver", deref(e.Driver)), e.Notes)
	default:
		p.Comment = joinComment(reason, e.Notes)
	}
	return p
}

func (f *seedFile) parts(status, source string) []Part {
	var out []Part
	add := func(cat string, es []seedEntry) {
		for _, e := range es {
			p := convert(cat, e, status, source)
			if source == SourceSWAnalytics && e.Count > 0 {
				p.Comment = joinComment(p.Comment, fmt.Sprintf("SW Analytics count: %d.", e.Count))
			}
			out = append(out, p)
		}
	}
	add(CatServer, f.ServerModels)
	add(CatCPU, f.CPUs)
	add(CatNIC, f.NICs)
	add(CatNIC, f.NICs10G)
	add(CatNIC, f.NICs1G)
	add(CatDrive, f.Drives)
	add(CatController, f.Controllers)
	return out
}

// Seed returns the datasheet parts. A part in both lists is kept once, as
// unsupported (the safer answer), with a note that the supported sheet also
// lists it.
func Seed() ([]Part, error) {
	var sup, uns seedFile
	for name, dst := range map[string]*seedFile{
		"seed/supported-hardware.json": &sup, "seed/unsupported-hardware.json": &uns,
	} {
		b, err := seedFS.ReadFile(name)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, dst); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
	}
	out := uns.parts(StatusUnsupported, SourceDatasheet)
	index := map[string]int{}
	for i := range out {
		for _, k := range out[i].Keys() {
			index[out[i].Category+"/"+k] = i
		}
	}
	for _, p := range sup.parts(StatusSupported, SourceDatasheet) {
		dup := -1
		for _, k := range p.Keys() {
			if i, ok := index[p.Category+"/"+k]; ok {
				dup = i
				break
			}
		}
		if dup >= 0 {
			u := &out[dup]
			u.Comment = joinComment(u.Comment, "Also listed as supported in the Supported Hardware sheets.")
			for _, a := range p.Aliases {
				u.Aliases = appendUnique(u.Aliases, a)
			}
			continue
		}
		// Supported entries may share a source text ("Dual E5-2609 v3/v4" is
		// both CPUs), so they are only checked against the unsupported list.
		out = append(out, p)
	}
	return out, nil
}

func appendUnique(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

// SWAnalytics returns the hardware reported by SERVERware installations
// (SW Analytics), filtered to physical server hardware
// (~/claude/Resources/Hardware/build_sw_analytics.py). Parts the list already
// has are merged into the existing entry by the store.
func SWAnalytics() ([]Part, error) {
	f, err := swAnalyticsFile()
	if err != nil {
		return nil, err
	}
	return f.parts(StatusSupported, SourceSWAnalytics), nil
}

// SWAnalyticsRemoved lists parts of the first SW Analytics list that were
// taken out later (desktop CPUs and boards); only Category and Name are set.
func SWAnalyticsRemoved() ([]Part, error) {
	f, err := swAnalyticsFile()
	if err != nil {
		return nil, err
	}
	var out []Part
	for _, r := range f.Removed {
		out = append(out, Part{Category: r.Type, Name: r.Name})
	}
	return out, nil
}

func swAnalyticsFile() (*seedFile, error) {
	b, err := seedFS.ReadFile("seed/sw-analytics-hardware.json")
	if err != nil {
		return nil, err
	}
	var f seedFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("sw-analytics-hardware.json: %w", err)
	}
	return &f, nil
}
