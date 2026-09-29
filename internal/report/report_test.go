package report

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func sampleJSON(t *testing.T) []byte {
	t.Helper()
	b, err := json.Marshal(Sample(SampleHosts[0], 1, time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSampleIsValid(t *testing.T) {
	for i, h := range SampleHosts {
		b, _ := json.Marshal(Sample(h, uint64(i+1), time.Now()))
		r, err := Parse(b)
		if err != nil {
			t.Fatalf("host %d: %v", i, err)
		}
		if len(r.Tests) != 7 {
			t.Fatalf("host %d: %d tests", i, len(r.Tests))
		}
	}
}

// mutate decodes the sample into a generic map, applies f, and re-encodes.
func mutate(t *testing.T, f func(m map[string]any)) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(sampleJSON(t), &m); err != nil {
		t.Fatal(err)
	}
	f(m)
	b, _ := json.Marshal(m)
	return b
}

func test0(m map[string]any) map[string]any { return m["tests"].([]any)[0].(map[string]any) }

func TestValidationErrors(t *testing.T) {
	cases := []struct {
		name, field string
		f           func(m map[string]any)
	}{
		{"no schema", "schema_version", func(m map[string]any) { delete(m, "schema_version") }},
		{"schema 2", "schema_version", func(m map[string]any) { m["schema_version"] = 2 }},
		{"bad uuid", "report_id", func(m map[string]any) { m["report_id"] = "abc" }},
		{"bad created", "created_at", func(m map[string]any) { m["created_at"] = "yesterday" }},
		{"no profile", "profile.name", func(m map[string]any) { m["profile"] = map[string]any{"version": 1} }},
		{"profile v0", "profile.version", func(m map[string]any) { m["profile"] = map[string]any{"name": "standard", "version": 0} }},
		{"no tests", "tests", func(m map[string]any) { m["tests"] = []any{} }},
		{"bad mode", "tests[0].mode", func(m map[string]any) { test0(m)["mode"] = "burst" }},
		{"bad stop", "tests[0].result.stop_reason", func(m map[string]any) {
			test0(m)["result"].(map[string]any)["stop_reason"] = "tired"
		}},
		{"dup id", "tests[1].id", func(m map[string]any) {
			m["tests"].([]any)[1].(map[string]any)["id"] = test0(m)["id"]
		}},
		{"type", "tests[0].result.max_concurrent_calls", func(m map[string]any) {
			test0(m)["result"].(map[string]any)["max_concurrent_calls"] = "many"
		}},
		{"series", "tests[0].timeseries.series.host_cpu_pct", func(m map[string]any) {
			test0(m)["timeseries"].(map[string]any)["series"].(map[string]any)["host_cpu_pct"] = "x"
		}},
		{"sw edition", "environment.serverware.edition", func(m map[string]any) {
			m["environment"].(map[string]any)["serverware"].(map[string]any)["edition"] = "huge"
		}},
	}
	for _, c := range cases {
		_, err := Parse(mutate(t, c.f))
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("%s: want ValidationError, got %v", c.name, err)
			continue
		}
		if ve.Field != c.field {
			t.Errorf("%s: field %q, want %q (%v)", c.name, ve.Field, c.field, err)
		}
	}
}

func TestUnknownFieldsAllowed(t *testing.T) {
	b := mutate(t, func(m map[string]any) { m["future_field"] = map[string]any{"x": 1} })
	if _, err := Parse(b); err != nil {
		t.Fatal(err)
	}
}

func TestNotJSON(t *testing.T) {
	_, err := Parse([]byte("{nope"))
	if err == nil || !strings.HasPrefix(err.Error(), "body:") {
		t.Fatalf("got %v", err)
	}
}

func TestSummary(t *testing.T) {
	r, err := Parse(sampleJSON(t))
	if err != nil {
		t.Fatal(err)
	}
	s := r.Summary()
	if s.Disks != "2x 960 GB nvme SAMSUNG MZQL2960HCJR" {
		t.Errorf("disks %q", s.Disks)
	}
	if s.PBXware != "CC 8.2.0.0, MT 8.2.0.0" {
		t.Errorf("pbxware %q", s.PBXware)
	}
	if len(s.Results) != 7 {
		t.Errorf("results %d", len(s.Results))
	}
}

func TestHardwareReports(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	for _, script := range []bool{false, true} {
		b, _ := json.Marshal(SampleHardware(SampleHosts[0], 7, now, script))
		r, err := Parse(b)
		if err != nil {
			t.Fatalf("script=%v: %v", script, err)
		}
		s := r.Summary()
		if s.Kind != KindHardware || s.TestCount != 0 {
			t.Errorf("script=%v: kind %q, %d tests", script, s.Kind, s.TestCount)
		}
		wantSrc := map[bool]string{false: "swarmdialer", true: "hw-collect"}[script]
		if s.SourceName != wantSrc || s.SourceVersion == "" {
			t.Errorf("script=%v: source %q %q", script, s.SourceName, s.SourceVersion)
		}
	}
	// The script's exact JSON shape, as documented in Project_HW_collect.md.
	raw := `{"schema_version":1,"report_id":"0b0c8a52-3b7e-4c1a-9d2e-6b8f0a1c2d3e","created_at":"2026-09-29T10:00:00Z",
	"source":{"name":"hw-collect","version":"1.0.0"},"profile":{"name":"hardware","version":1},
	"environment":{"serverware":{"version":"unknown","edition":"mirror"},"host":{"cpu_model":"Intel(R) Xeon(R) Silver 4208 CPU @ 2.10GHz",
	"cpu_sockets":2,"cpu_cores":16,"cpu_threads":32,"cpu_max_mhz":3200,"memory_bytes":201326592000,
	"disks":[{"model":"SAMSUNG MZ7LH960HAJR-00005","size_bytes":0,"type":"ssd"}],"network":[],"storage_controllers":[],"nics":[],"bonds":[]},
	"vps":{},"pbxware":[]},"tests":[]}`
	if _, err := Parse([]byte(raw)); err != nil {
		t.Fatalf("script JSON: %v", err)
	}
}

func TestHardwareValidation(t *testing.T) {
	cases := []struct {
		name, field string
		f           func(m map[string]any)
	}{
		{"benchmark without tests", "tests", func(m map[string]any) { m["tests"] = []any{} }},
		{"hardware with tests", "tests", func(m map[string]any) { m["profile"] = map[string]any{"name": "hardware", "version": 1} }},
		{"no version at all", "swarmdialer_version", func(m map[string]any) { delete(m, "swarmdialer_version") }},
		{"bad source", "source.name", func(m map[string]any) { m["source"] = map[string]any{"name": "", "version": "1"} }},
		{"hardware without cpu", "environment.host.cpu_model", func(m map[string]any) {
			m["profile"] = map[string]any{"name": "hardware", "version": 1}
			m["tests"] = []any{}
			m["environment"].(map[string]any)["host"].(map[string]any)["cpu_model"] = ""
		}},
	}
	for _, c := range cases {
		_, err := Parse(mutate(t, c.f))
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Field != c.field {
			t.Errorf("%s: got %v, want field %q", c.name, err, c.field)
		}
	}
	// target_not_reached is a valid stop reason.
	b := mutate(t, func(m map[string]any) { test0(m)["result"].(map[string]any)["stop_reason"] = "target_not_reached" })
	if _, err := Parse(b); err != nil {
		t.Fatalf("target_not_reached: %v", err)
	}
}
