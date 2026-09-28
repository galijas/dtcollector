package report

import (
	"fmt"
	"sort"
	"strings"
)

// Summary holds the fields the report list shows and filters on; they are
// stored as indexed columns next to the full report.
type Summary struct {
	ReportID           string
	CreatedAt          string
	SchemaVersion      int
	ProfileName        string
	ProfileVersion     int
	SwarmDialerVersion string
	ServerwareVersion  string
	ServerwareEdition  string
	CPUModel           string
	CPUSockets         int
	CPUCores           int
	CPUThreads         int
	MemoryBytes        int64
	Disks              string
	PBXware            string
	TestCount          int
	Results            []TestResult
}

type TestResult struct {
	ID                 string `json:"id"`
	MaxConcurrentCalls int    `json:"max_calls"`
	StopReason         string `json:"stop_reason"`
}

func (r *Report) Summary() Summary {
	h := r.Environment.Host
	s := Summary{
		ReportID:           strings.ToLower(r.ReportID),
		CreatedAt:          r.CreatedAt,
		SchemaVersion:      r.SchemaVersion,
		ProfileName:        r.Profile.Name,
		ProfileVersion:     r.Profile.Version,
		SwarmDialerVersion: r.SwarmDialerVersion,
		ServerwareVersion:  r.Environment.Serverware.Version,
		ServerwareEdition:  r.Environment.Serverware.Edition,
		CPUModel:           h.CPUModel,
		CPUSockets:         h.CPUSockets,
		CPUCores:           h.CPUCores,
		CPUThreads:         h.CPUThreads,
		MemoryBytes:        h.MemoryBytes,
		Disks:              DiskSummary(h.Disks),
		PBXware:            PBXwareSummary(r.Environment.PBXware),
		TestCount:          len(r.Tests),
	}
	for _, t := range r.Tests {
		s.Results = append(s.Results, TestResult{t.ID, t.Result.MaxConcurrentCalls, t.Result.StopReason})
	}
	return s
}

// DiskSummary groups identical disks: "2x 960 GB nvme SAMSUNG MZQL2960".
func DiskSummary(disks []Disk) string {
	counts := map[string]int{}
	var order []string
	for _, d := range disks {
		parts := []string{}
		if d.SizeBytes > 0 {
			parts = append(parts, HumanBytes(d.SizeBytes, 1000))
		}
		if d.Type != "" {
			parts = append(parts, d.Type)
		}
		if d.Model != "" {
			parts = append(parts, d.Model)
		}
		k := strings.Join(parts, " ")
		if k == "" {
			k = "unknown disk"
		}
		if counts[k] == 0 {
			order = append(order, k)
		}
		counts[k]++
	}
	out := make([]string, 0, len(order))
	for _, k := range order {
		out = append(out, fmt.Sprintf("%dx %s", counts[k], k))
	}
	return strings.Join(out, ", ")
}

// PBXwareSummary lists "role version" pairs, sorted by role: "CC 8.2.0.0, MT 8.2.0.0".
func PBXwareSummary(p []PBXware) string {
	out := make([]string, 0, len(p))
	for _, x := range p {
		out = append(out, strings.TrimSpace(x.Role+" "+x.Version))
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// HumanBytes formats a byte count with base 1000 (disks) or 1024 (memory).
func HumanBytes(n int64, base int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	if base == 1024 {
		units = []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	}
	v := float64(n)
	i := 0
	for v >= float64(base) && i < len(units)-1 {
		v /= float64(base)
		i++
	}
	if i == 0 || v >= 100 {
		return fmt.Sprintf("%.0f %s", v, units[i])
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}
