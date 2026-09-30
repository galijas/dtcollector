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

func TestSeed(t *testing.T) {
	parts, err := Seed()
	if err != nil {
		t.Fatal(err)
	}
	count := map[string]int{}
	byName := map[string]Part{}
	for _, p := range parts {
		count[p.Category]++
		byName[p.Name] = p
		if !ValidCategory(p.Category) || !ValidStatus(p.Status) || p.Source != SourceDatasheet || p.Name == "" {
			t.Fatalf("bad part %+v", p)
		}
	}
	want := map[string]int{CatServer: 41, CatCPU: 61, CatNIC: 29, CatDrive: 19, CatController: 26}
	for c, n := range want {
		if count[c] != n {
			t.Errorf("%s: %d parts, want %d", c, count[c], n)
		}
	}
	flr := byName["HPE Ethernet 1Gb 4-port 331FLR"]
	if flr.Status != StatusUnsupported || flr.Attrs.Driver != "tg3" {
		t.Errorf("331FLR: %+v", flr)
	}
	if d := byName["Samsung PM9A3"]; d.Attrs.Type != "NVMe SSD" {
		t.Errorf("PM9A3 type %q", d.Attrs.Type)
	}
	if c := byName["Dell PERC H730P"]; c.Comment == "" {
		t.Error("controller details should be in the comment")
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

func TestSWAnalytics(t *testing.T) {
	parts, err := SWAnalytics()
	if err != nil {
		t.Fatal(err)
	}
	count := map[string]int{}
	for _, p := range parts {
		count[p.Category]++
		if p.Source != SourceSWAnalytics || p.Status != StatusSupported || p.Name == "" {
			t.Fatalf("bad part %+v", p)
		}
	}
	want := map[string]int{CatServer: 93, CatCPU: 125, CatNIC: 42, CatDrive: 16, CatController: 23}
	for c, n := range want {
		if count[c] != n {
			t.Errorf("%s: %d parts, want %d", c, count[c], n)
		}
	}
	if Key(CatCPU, "Intel(R) Xeon(R) CPU E5-2630 0 @ 2.30GHz") != Key(CatCPU, "Intel Xeon E5-2630") {
		t.Error("the first-generation E5 \" 0\" suffix must not affect matching")
	}
}
