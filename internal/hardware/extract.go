package hardware

import (
	"regexp"
	"strings"

	"dtcollector/internal/report"
)

// placeholders are DMI filler values that name no real product.
// "ProductCode" is the model cheap USB card readers report.
var placeholderRe = regexp.MustCompile(`(?i)^(to be filled.*|system product name|default string|not specified|unknown|none|n/a|0+|productcode)$`)

func usable(s string) bool {
	s = tidy(s)
	return s != "" && !placeholderRe.MatchString(s)
}

// virtualRe marks virtual devices (BMC virtual media, hypervisor disks and
// NICs, VM platforms), which are not hardware to validate.
var virtualRe = regexp.MustCompile(`(?i)virtual|virtio|qemu|vmware|vbox`)

// removableRe marks removable and optical media (USB flash disks, card
// readers, SD modules, CD/DVD/BD drives), which reports list as drives and
// sometimes as storage controllers. They are not hardware to validate.
var removableRe = regexp.MustCompile(`(?i)\bUSB|Flash Disk|Flash Drive|DataTraveler|Cruzer|UDisk|JetFlash|Card Reader|SD/MMC|Media Reader|Mass Storage|STORE N|Internal Dual SD|IDSDM|` +
	`\bDVD|\bCD-?(ROM|RW|R)\b|\bBD-?(ROM|RE|RW|R)\b|Blu-?ray|HL-DT-ST|TSSTcorp|PLDS|Optiarc|Slimtype`)

// Removable reports whether a part name (or a raw name it was seen as) is
// removable or optical media, or a placeholder model such as "ProductCode".
func Removable(names ...string) bool {
	for _, n := range names {
		if removableRe.MatchString(n) || placeholderRe.MatchString(tidy(n)) {
			return true
		}
	}
	return false
}

// Storage controllers follow the SW Analytics rules: only RAID/HBA cards and
// boot RAID devices are hardware to validate. Each pattern is matched
// against the part's name and the raw name it was reported as.
var controllerSkipRes = []*regexp.Regexp{
	regexp.MustCompile(`^\s*Linux\s*$`), // NVMe-over-TCP target
	regexp.MustCompile(`(?i)\bUSB|Flash Drive|DataTraveler|Cruzer|UDisk|-CRW\b|Media Reader|JetFlash|STORE N|SanDisk 3\.2|Mass Storage|Ours Technology|Internal Dual SD`), // USB storage, card readers, SD modules
	regexp.MustCompile(`(?i)virtual|iDRAC|DRAC5|\biLO\b|floppy|Avocent`),                                                                                                  // BMC virtual media, floppy
	regexp.MustCompile(`(?i)virtio|PVSCSI|VMware|53c1030`),                                                                                                                // virtual (and VMware-emulated) controllers
	regexp.MustCompile(`(?i)\bAHCI\b|\bIDE\b|Storage Control Unit|ASM106\d|SB7x0|Chipset SATA Controller`),                                                                // chipset SATA/AHCI/IDE
	regexp.MustCompile(`(?i)RAID mode|SATA RAID Controller|sSATA Controller`),                                                                                             // chipset SATA in RAID mode
	regexp.MustCompile(`(?i)Volume Management Device`),                                                                                                                    // Intel VMD (CPU-integrated)
	regexp.MustCompile(`(?i)iSCSI Initiator`),                                                                                                                             // iSCSI function of a NIC
	regexp.MustCompile(`^\s*Intel Corporation Intel Corporation\s*$|^\s*Intel Corporation\s*$`),                                                                           // broken entry
	// NVMe drives listed as controllers; reports already list them as drives.
	regexp.MustCompile(`(?i)SSDPE|SSDPF|MZQL|MZVL|Micron_|7450 PRO NVMe|SEDC3000|NVMe SSD Controller|SSD 9[78]0|970 EVO|SN850X|HWE3\d`),
}

// SkipController reports whether a storage controller name (or a raw name it
// was seen as) is one the list leaves out.
func SkipController(names ...string) bool {
	for _, n := range names {
		for _, re := range controllerSkipRes {
			if re.MatchString(n) {
				return true
			}
		}
	}
	return false
}

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
		names := append([]string{p.Name}, p.Aliases...)
		if IsVirtual(names...) || ((p.Category == CatDrive || p.Category == CatController) && Removable(names...)) ||
			(p.Category == CatController && SkipController(names...)) {
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
