// Package report defines report format version 1, the upload contract with
// SwarmDialer (see docs/api.md), and validates uploaded reports.
package report

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const SchemaVersion = 1

type Report struct {
	SchemaVersion      int         `json:"schema_version"`
	ReportID           string      `json:"report_id"`
	CreatedAt          string      `json:"created_at"`
	SwarmDialerVersion string      `json:"swarmdialer_version"`
	Profile            Profile     `json:"profile"`
	Environment        Environment `json:"environment"`
	Tests              []Test      `json:"tests"`
}

type Profile struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
}

type Environment struct {
	Serverware Serverware           `json:"serverware"`
	Host       Host                 `json:"host"`
	VPS        map[string]VPSLimits `json:"vps"`
	PBXware    []PBXware            `json:"pbxware"`
}

type Serverware struct {
	Version string `json:"version"`
	Edition string `json:"edition"`
}

type Host struct {
	CPUModel    string  `json:"cpu_model"`
	CPUSockets  int     `json:"cpu_sockets"`
	CPUCores    int     `json:"cpu_cores"`
	CPUThreads  int     `json:"cpu_threads"`
	CPUMaxMHz   float64 `json:"cpu_max_mhz"`
	MemoryBytes int64   `json:"memory_bytes"`
	Disks       []Disk  `json:"disks"`
	Network     []NIC   `json:"network"`
}

type Disk struct {
	Model     string `json:"model"`
	SizeBytes int64  `json:"size_bytes"`
	Type      string `json:"type"`
}

type NIC struct {
	SpeedMbps float64 `json:"speed_mbps"`
}

type VPSLimits struct {
	CPULimit     float64  `json:"cpu_limit"`
	CPUShare     *float64 `json:"cpu_share,omitempty"`
	MemLimitMB   float64  `json:"mem_limit_mb"`
	CallrecRAMMB *float64 `json:"callrec_ram_mb,omitempty"`
}

type PBXware struct {
	Role            string `json:"role"`
	Version         string `json:"version"`
	Edition         string `json:"edition"`
	LicenseChannels int    `json:"license_channels"`
}

type Test struct {
	ID              string     `json:"id"`
	Mode            string     `json:"mode"`
	CallType        string     `json:"call_type"`
	Codec           Codec      `json:"codec"`
	Recording       string     `json:"recording"`
	RecordingFormat string     `json:"recording_format"`
	CallDurationS   float64    `json:"call_duration_s"`
	DialRateCPS     float64    `json:"dial_rate_cps"`
	StartedAt       string     `json:"started_at"`
	FinishedAt      string     `json:"finished_at"`
	Result          Result     `json:"result"`
	Timeseries      Timeseries `json:"timeseries"`
}

type Codec struct {
	Caller string `json:"caller"`
	Callee string `json:"callee"`
}

type Result struct {
	StopReason             string           `json:"stop_reason"`
	MaxConcurrentCalls     int              `json:"max_concurrent_calls"`
	Calls                  Calls            `json:"calls"`
	SetupMS                Stats            `json:"setup_ms"`
	MOS                    MOS              `json:"mos"`
	RTPReceivedRatio       *float64         `json:"rtp_received_ratio"`
	QualityDegradedAtCalls *int             `json:"quality_degraded_at_calls"`
	AtTarget               AtTarget         `json:"at_target"`
	Recording              *RecordingResult `json:"recording,omitempty"`
}

type Calls struct {
	Started  int `json:"started"`
	Answered int `json:"answered"`
	Failed   int `json:"failed"`
}

type Stats struct {
	Avg float64 `json:"avg"`
	P95 float64 `json:"p95"`
	Max float64 `json:"max"`
}

type MOS struct {
	Avg float64 `json:"avg"`
	Min float64 `json:"min"`
}

type AtTarget struct {
	HostCPUPct     float64            `json:"host_cpu_pct"`
	HostMemPct     float64            `json:"host_mem_pct"`
	AsteriskCPUPct map[string]float64 `json:"asterisk_cpu_pct"`
}

type RecordingResult struct {
	RamdiskFullEstimatedAtCalls *int      `json:"ramdisk_full_estimated_at_calls"`
	RamdiskFullEstimatedAt      *string   `json:"ramdisk_full_estimated_at"`
	MP3ConversionDelayS         *MP3Delay `json:"mp3_conversion_delay_s"`
}

type MP3Delay struct {
	Avg   float64 `json:"avg"`
	P95   float64 `json:"p95"`
	Max   float64 `json:"max"`
	Trend string  `json:"trend"`
}

type Timeseries struct {
	IntervalS float64                    `json:"interval_s"`
	Start     string                     `json:"start"`
	Series    map[string]json.RawMessage `json:"series"`
}

const (
	maxTests        = 50
	maxSeriesPoints = 200000
)

var (
	uuidRe  = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	indexRe = regexp.MustCompile(`\.(\d+)\b`)
	identRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

	modes       = []string{"ramp", "rolling"}
	recordings  = []string{"off", "mono", "stereo"}
	swEditions  = []string{"standalone", "mirror", "cluster"}
	trends      = []string{"stable", "growing"}
	stopReasons = []string{"target_reached", "host_cpu_100", "host_ram_100", "vps_cpu_limit",
		"vps_ram_limit", "swarmdialer_overloaded", "error"}
)

// ValidationError names the offending field; its message is returned to the
// uploader as the 400 body.
type ValidationError struct {
	Field string
	Msg   string
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Msg }

func fail(field, format string, args ...any) error {
	return &ValidationError{Field: field, Msg: fmt.Sprintf(format, args...)}
}

// Parse decodes and validates a report. Unknown fields are allowed (and kept
// in the stored raw JSON) so SwarmDialer can add optional fields within v1.
func Parse(raw []byte) (*Report, error) {
	var probe struct {
		SchemaVersion *int `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, decodeError(err)
	}
	if probe.SchemaVersion == nil {
		return nil, fail("schema_version", "missing")
	}
	if *probe.SchemaVersion != SchemaVersion {
		return nil, fail("schema_version", "unsupported version %d (supported: %d)", *probe.SchemaVersion, SchemaVersion)
	}
	var r Report
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, decodeError(err)
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return &r, nil
}

func decodeError(err error) error {
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) && te.Field != "" {
		return fail(indexRe.ReplaceAllString(te.Field, "[$1]"), "expected %s, got JSON %s", te.Type.String(), te.Value)
	}
	return fail("body", "invalid JSON: %v", err)
}

func (r *Report) Validate() error {
	if !uuidRe.MatchString(r.ReportID) {
		return fail("report_id", "must be a UUID")
	}
	if _, err := parseTime(r.CreatedAt); err != nil {
		return fail("created_at", "must be an RFC 3339 timestamp")
	}
	if strings.TrimSpace(r.SwarmDialerVersion) == "" || len(r.SwarmDialerVersion) > 64 {
		return fail("swarmdialer_version", "required (max 64 characters)")
	}
	if !identRe.MatchString(r.Profile.Name) {
		return fail("profile.name", "required: 1-64 characters of letters, digits, '.', '_' or '-'")
	}
	if r.Profile.Version < 1 {
		return fail("profile.version", "must be 1 or greater")
	}
	if err := r.Environment.validate(); err != nil {
		return err
	}
	if len(r.Tests) == 0 {
		return fail("tests", "at least one test is required")
	}
	if len(r.Tests) > maxTests {
		return fail("tests", "at most %d tests allowed", maxTests)
	}
	seen := map[string]bool{}
	for i := range r.Tests {
		if err := r.Tests[i].validate(fmt.Sprintf("tests[%d]", i)); err != nil {
			return err
		}
		if seen[r.Tests[i].ID] {
			return fail(fmt.Sprintf("tests[%d].id", i), "duplicate test id %q", r.Tests[i].ID)
		}
		seen[r.Tests[i].ID] = true
	}
	return nil
}

func (e *Environment) validate() error {
	if strings.TrimSpace(e.Serverware.Version) == "" {
		return fail("environment.serverware.version", "required")
	}
	if e.Serverware.Edition != "" && !oneOf(e.Serverware.Edition, swEditions) {
		return fail("environment.serverware.edition", "must be one of %s", strings.Join(swEditions, ", "))
	}
	h := e.Host
	if h.CPUSockets < 0 || h.CPUCores < 0 || h.CPUThreads < 0 || h.CPUMaxMHz < 0 || h.MemoryBytes < 0 {
		return fail("environment.host", "numeric values must not be negative")
	}
	if len(h.Disks) > 256 || len(h.Network) > 256 {
		return fail("environment.host", "too many disks or network interfaces")
	}
	for i, p := range e.PBXware {
		if strings.TrimSpace(p.Role) == "" {
			return fail(fmt.Sprintf("environment.pbxware[%d].role", i), "required")
		}
	}
	return nil
}

func (t *Test) validate(path string) error {
	if !identRe.MatchString(t.ID) {
		return fail(path+".id", "required: 1-64 characters of letters, digits, '.', '_' or '-'")
	}
	if !oneOf(t.Mode, modes) {
		return fail(path+".mode", "must be one of %s", strings.Join(modes, ", "))
	}
	if !oneOf(t.Recording, recordings) {
		return fail(path+".recording", "must be one of %s", strings.Join(recordings, ", "))
	}
	started, err := parseTime(t.StartedAt)
	if err != nil {
		return fail(path+".started_at", "must be an RFC 3339 timestamp")
	}
	finished, err := parseTime(t.FinishedAt)
	if err != nil {
		return fail(path+".finished_at", "must be an RFC 3339 timestamp")
	}
	if finished.Before(started) {
		return fail(path+".finished_at", "is before started_at")
	}
	if t.CallDurationS < 0 || t.DialRateCPS < 0 {
		return fail(path, "call_duration_s and dial_rate_cps must not be negative")
	}
	res := t.Result
	if !oneOf(res.StopReason, stopReasons) {
		return fail(path+".result.stop_reason", "must be one of %s", strings.Join(stopReasons, ", "))
	}
	if res.MaxConcurrentCalls < 0 {
		return fail(path+".result.max_concurrent_calls", "must not be negative")
	}
	if rec := res.Recording; rec != nil && rec.MP3ConversionDelayS != nil {
		if tr := rec.MP3ConversionDelayS.Trend; tr != "" && !oneOf(tr, trends) {
			return fail(path+".result.recording.mp3_conversion_delay_s.trend", "must be one of %s", strings.Join(trends, ", "))
		}
	}
	if rec := res.Recording; rec != nil && rec.RamdiskFullEstimatedAt != nil {
		if _, err := parseTime(*rec.RamdiskFullEstimatedAt); err != nil {
			return fail(path+".result.recording.ramdisk_full_estimated_at", "must be an RFC 3339 timestamp or null")
		}
	}
	return t.Timeseries.validate(path + ".timeseries")
}

func (ts *Timeseries) validate(path string) error {
	if len(ts.Series) == 0 {
		return nil
	}
	if ts.IntervalS <= 0 {
		return fail(path+".interval_s", "must be greater than 0")
	}
	if _, err := parseTime(ts.Start); err != nil {
		return fail(path+".start", "must be an RFC 3339 timestamp")
	}
	for name, raw := range ts.Series {
		if err := validateSeries(path+".series."+name, raw); err != nil {
			return err
		}
	}
	return nil
}

// A series is either an array of numbers (null for a missing sample) or an
// object mapping a group name (MT, CC, swarmdialer) to such an array.
func validateSeries(path string, raw json.RawMessage) error {
	var arr []*float64
	if err := json.Unmarshal(raw, &arr); err == nil {
		if len(arr) > maxSeriesPoints {
			return fail(path, "too many points (max %d)", maxSeriesPoints)
		}
		return nil
	}
	var groups map[string][]*float64
	if err := json.Unmarshal(raw, &groups); err != nil {
		return fail(path, "must be an array of numbers or an object of arrays of numbers")
	}
	for g, a := range groups {
		if len(a) > maxSeriesPoints {
			return fail(path+"."+g, "too many points (max %d)", maxSeriesPoints)
		}
	}
	return nil
}

func parseTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, s)
}

func oneOf(s string, allowed []string) bool {
	for _, a := range allowed {
		if s == a {
			return true
		}
	}
	return false
}
