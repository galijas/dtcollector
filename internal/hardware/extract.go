package hardware

import (
	"regexp"
	"strings"

	"dtcollector/internal/report"
)

// placeholders are DMI filler values that name no real product.
var placeholderRe = regexp.MustCompile(`(?i)^(to be filled.*|system product name|default string|not specified|unknown|none|n/a|0+)$`)

func usable(s string) bool {
	s = tidy(s)
	return s != "" && !placeholderRe.MatchString(s)
}

// virtualRe marks virtual devices (BMC virtual media, hypervisor disks and
// NICs, VM platforms), which are not hardware to validate.
var virtualRe = regexp.MustCompile(`(?i)virtual|virtio|qemu|vmware|vbox`)

// IsVirtual reports whether a part name (or a raw name it was seen as) is a
// virtual device.
func IsVirtual(names ...string) bool {
	for _, n := range names {
		if virtualRe.MatchString(n) {
			return true
		}
	}
	return false
}

// FromReport lists the hardware parts a report describes: server model,
// CPU, NIC models, drives and storage controllers. They are added as
// supported: the report shows the hardware running SERVERware.
func FromReport(r *report.Report) []Part {
	h := r.Environment.Host
	var out []Part
	seen := map[string]bool{}
	add := func(p Part) {
		if IsVirtual(append([]string{p.Name}, p.Aliases...)...) {
			return
		}
		p.Status = StatusSupported
		k := p.Category + "/" + Key(p.Category, p.Name)
		if seen[k] {
			for i := range out {
				if out[i].Category == p.Category && Key(p.Category, out[i].Name) == Key(p.Category, p.Name) {
					for _, n := range p.Attrs.Ports {
						out[i].Attrs.Ports = AddPort(out[i].Attrs.Ports, n)
					}
				}
			}
			return
		}
		seen[k] = true
		out = append(out, p)
	}
	raw := func(parts ...string) []string {
		s := tidy(strings.Join(parts, " "))
		if s == "" {
			return nil
		}
		return []string{s}
	}

	if usable(h.SystemModel) {
		add(Part{Category: CatServer, Name: JoinName(CleanVendor(h.SystemVendor), h.SystemModel),
			Aliases: raw(h.SystemVendor, h.SystemModel)})
	}
	if usable(h.CPUModel) {
		add(Part{Category: CatCPU, Name: CleanCPU(h.CPUModel), Aliases: raw(h.CPUModel)})
	}
	for _, n := range h.NICs {
		if !usable(n.Product) {
			continue
		}
		a := Attrs{Speed: SpeedLabel(n.SpeedMbps), Driver: tidy(n.Driver)}
		if n.Count > 0 {
			a.Ports = []int{n.Count}
		}
		add(Part{Category: CatNIC, Name: JoinName(CleanVendor(n.Vendor), n.Product), Attrs: a,
			Aliases: raw(n.Vendor, n.Product)})
	}
	for _, d := range h.Disks {
		model := tidy(strings.ReplaceAll(d.Model, "_", " "))
		if !usable(model) {
			continue
		}
		add(Part{Category: CatDrive, Name: model, Attrs: Attrs{Type: DriveType(d.Type)}, Aliases: raw(d.Model)})
	}
	for _, c := range h.StorageControllers {
		if !usable(c.Product) {
			continue
		}
		add(Part{Category: CatController, Name: JoinName(CleanVendor(c.Vendor), c.Product), Aliases: raw(c.Vendor, c.Product)})
	}
	return out
}

// ReportSource is the list source for parts found in a report: hardware-only
// uploads (SWHW Collector, SwarmDialer "Upload Hardware Info Only") are SWHW
// data, benchmark runs are Test Script.
func ReportSource(r *report.Report) string {
	if r.IsHardware() {
		return SourceSWHW
	}
	return SourceTestScript
}
