package hardware

import (
	"testing"
	"time"

	"dtcollector/internal/report"
)

func TestKey(t *testing.T) {
	same := [][3]string{
		{CatCPU, "Intel(R) Xeon(R) CPU E5-2699 v4 @ 2.20GHz", "Intel Xeon E5-2699 v4"},
		{CatCPU, "AMD EPYC 7443P 24-Core Processor", "AMD EPYC 7443P"},
		{CatNIC, "Intel Corporation I350 Gigabit Network Connection", "Intel I350 Gigabit Network Connection"},
		{CatNIC, "Broadcom Inc. and subsidiaries NetXtreme BCM5720", "Broadcom NetXtreme BCM5720"},
		{CatServer, "Supermicro SYS-1029TP-DC0R", "supermicro sys 1029tp dc0r"},
	}
	for _, c := range same {
		if Key(c[0], c[1]) != Key(c[0], c[2]) {
			t.Errorf("%q and %q differ: %q vs %q", c[1], c[2], Key(c[0], c[1]), Key(c[0], c[2]))
		}
	}
	if Key(CatNIC, "Intel X710") == Key(CatNIC, "Intel XL710") {
		t.Error("X710 and XL710 must differ")
	}
}

func TestCleaning(t *testing.T) {
	cases := map[string]string{
		CleanCPU("Intel(R) Xeon(R) Silver 4208 CPU @ 2.10GHz"): "Intel Xeon Silver 4208",
		CleanCPU("AMD EPYC 7443P 24-Core Processor"):           "AMD EPYC 7443P",
		CleanVendor("Intel Corporation"):                       "Intel",
		CleanVendor("Super Micro Computer Inc"):                "Supermicro",
		CleanVendor("Dell Inc."):                               "Dell",
		CleanVendor("Broadcom Inc. and subsidiaries"):          "Broadcom",
		JoinName("Intel", "Intel X550"):                        "Intel X550",
		SpeedLabel(10000):                                      "10GbE",
		SpeedLabel(2500):                                       "2.5GbE",
		DriveType("nvme"):                                      "NVMe SSD",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	if p, err := ParsePorts("4, 2 2"); err != nil || len(p) != 2 || p[0] != 2 || p[1] != 4 {
		t.Errorf("ParsePorts: %v %v", p, err)
	}
	if _, err := ParsePorts("two"); err == nil {
		t.Error("ParsePorts accepted text")
	}
}

func TestStarterList(t *testing.T) {
	parts, err := StarterList()
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 370 {
		t.Errorf("%d parts, want 370", len(parts))
	}
	byName := map[string]Part{}
	seen := map[string]bool{}
	for _, p := range parts {
		byName[p.Name] = p
		k := p.Category + "/" + Key(p.Category, p.Name)
		if seen[k] {
			t.Errorf("duplicate %s", k)
		}
		seen[k] = true
		if p.Source != SourceDatasheet && p.Source != SourceSWAnalytics {
			t.Errorf("%s: unexpected source %q", p.Name, p.Source)
		}
	}
	if p := byName["Dell BOSS-S1"]; p.Status != StatusUnsupported || p.Comment != "Boot issues encountered previously" {
		t.Errorf("BOSS-S1: %+v", p)
	}
	if p := byName["Marvell 88SE9230 SATA RAID"]; p.Status != StatusUnverified || p.UpdatedBy != "admin" {
		t.Errorf("edited entry lost: %+v", p)
	}
	if p := byName["Intel X550"]; p.Attrs.Driver != "ixgbe" || len(p.Aliases) == 0 {
		t.Errorf("attrs/aliases lost: %+v", p)
	}
}

func TestFromReport(t *testing.T) {
	r := report.SampleHardware(report.SampleHosts[1], 3, time.Now(), true)
	r.Environment.Host.Disks = append(r.Environment.Host.Disks, report.Disk{Model: "SAMSUNG_MZ7LH960HAJR-00005", Type: "ssd"})
	parts := FromReport(r)
	got := map[string]Part{}
	for _, p := range parts {
		got[p.Category+"|"+p.Name] = p
	}
	for _, k := range []string{
		"server_model|Supermicro SYS-6029BT-DNC0R", "cpu|AMD EPYC 7443P",
		"nic|Intel Ethernet Controller X550", "drive|Micron 5300 MTFD", "drive|SAMSUNG MZ7LH960HAJR-00005",
		"storage_controller|Broadcom / LSI SAS3008 PCI-Express Fusion-MPT SAS-3",
	} {
		if _, ok := got[k]; !ok {
			t.Errorf("missing %s (have %v)", k, keys(got))
		}
	}
	if n := got["nic|Intel Ethernet Controller X550"]; n.Attrs.Speed != "10GbE" || n.Attrs.Driver != "ixgbe" || n.Attrs.Ports[0] != 2 {
		t.Errorf("nic attrs %+v", n.Attrs)
	}
	for _, p := range parts {
		if p.Status != StatusSupported {
			t.Errorf("%s status %s", p.Name, p.Status)
		}
	}
	if ReportSource(r) != SourceSWHW {
		t.Error("hardware report source")
	}
}

func keys(m map[string]Part) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestFirstGenE5Key(t *testing.T) {
	if Key(CatCPU, "Intel(R) Xeon(R) CPU E5-2630 0 @ 2.30GHz") != Key(CatCPU, "Intel Xeon E5-2630") {
		t.Error("the first-generation E5 \" 0\" suffix must not affect matching")
	}
}

func TestVirtualDevicesSkipped(t *testing.T) {
	r := report.SampleHardware(report.SampleHosts[0], 5, time.Now(), true)
	h := &r.Environment.Host
	h.Disks = append(h.Disks, report.Disk{Model: "Virtual CD"}, report.Disk{Model: "Virtual Floppy"},
		report.Disk{Model: "QEMU HARDDISK"}, report.Disk{Model: "SAMSUNG MZ7LH480HAHQ-00005", Type: "ssd"})
	h.NICs = append(h.NICs, report.NICModel{Vendor: "Red Hat, Inc.", Product: "Virtio network device"})
	h.SystemVendor, h.SystemModel = "VMware, Inc.", "VMware Virtual Platform"
	names := map[string]bool{}
	for _, p := range FromReport(r) {
		names[p.Name] = true
		if IsVirtual(p.Name) {
			t.Errorf("virtual part kept: %s", p.Name)
		}
	}
	if !names["SAMSUNG MZ7LH480HAHQ-00005"] {
		t.Error("a real drive was dropped")
	}
	if names["VMware VMware Virtual Platform"] || names["VMware Virtual Platform"] {
		t.Error("VM platform kept as a server model")
	}
}
