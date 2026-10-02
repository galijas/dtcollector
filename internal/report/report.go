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

// HardwareProfile marks a hardware-only report: host inventory, no tests.
const HardwareProfile = "hardware"

type Report struct {
	SchemaVersion      int         `json:"schema_version"`
	ReportID           string      `json:"report_id"`
	CreatedAt          string      `json:"created_at"`
	SwarmDialerVersion string      `json:"swarmdialer_version"`
	Source             *Source     `json:"source,omitempty"`
	Profile            Profile     `json:"profile"`
	Environment        Environment `json:"environment"`
	Tests              []Test      `json:"tests"`
}

// Source names the uploading tool when it isn't SwarmDialer (e.g. the
// hw-collect script); SwarmDialer reports use swarmdialer_version instead.
type Source struct {
	Name    string `json:"name"`
	Version string `json:"version"`
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

	SystemVendor       string       `json:"system_vendor,omitempty"`
	SystemModel        string       `json:"system_model,omitempty"`
	StorageControllers []Controller `json:"storage_controllers,omitempty"`
	NICs               []NICModel   `json:"nics,omitempty"`
	Bonds              []Bond       `json:"bonds,omitempty"`
	Motherboard        *Motherboard `json:"motherboard,omitempty"`
}

type Controller struct {
	Vendor  string `json:"vendor"`
	Product string `json:"product"`
	Count   int    `json:"count"`
}

type NICModel struct {
	Vendor    string  `json:"vendor"`
	Product   string  `json:"product"`
	Driver    string  `json:"driver"`
	Count     int     `json:"count"`
	SpeedMbps float64 `json:"speed_mbps"`
}

type Bond struct {
	Ports int `json:"ports"`
}

type Motherboard struct {
	Vendor      string `json:"vendor"`
	Model       string `json:"model"`
	Version     string `json:"version,omitempty"`
	BIOSVendor  string `json:"bios_vendor,omitempty"`
	BIOSVersion string `json:"bios_version,omitempty"`
	BIOSDate    string `json:"bios_date,omitempty"`
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

	// Diagnostics, added within v1 by SwarmDialer 1.6.0 (all optional; see
	// ~/claude/SwarmDialer_report_diagnostics.md).
	StopDetail            string       `json:"stop_detail,omitempty"`
	QualityDegradedReason string       `json:"quality_degraded_reason,omitempty"`
	AtStop                *AtStop      `json:"at_stop,omitempty"`
	Failures              []Failure    `json:"failures,omitempty"`
	PBXware               *PBXwareView `json:"pbxware,omitempty"`
	Tool                  *ToolHealth  `json:"tool,omitempty"`
	Events                []Event      `json:"events,omitempty"`
}

// AtStop is the load in the 5-second sample that ended the test.
type AtStop struct {
	Calls             int                  `json:"calls"`
	HostCPUPct        float64              `json:"host_cpu_pct"`
	HostMemPct        float64              `json:"host_mem_pct"`
	HostIOWaitPct     float64              `json:"host_iowait_pct"`
	SwarmDialerCPUPct float64              `json:"swarmdialer_cpu_pct"`
	RAMDiskEstPct     *float64             `json:"ramdisk_est_pct,omitempty"`
	VPS               map[string]VPSAtStop `json:"vps,omitempty"`
}

type VPSAtStop struct {
	CPUPctOfLimit      *float64 `json:"cpu_pct_of_limit,omitempty"`
	CPUPctOfHost       float64  `json:"cpu_pct_of_host"`
	MemPctOfLimit      *float64 `json:"mem_pct_of_limit,omitempty"`
	MemBytes           float64  `json:"mem_bytes"`
	AsteriskCPUPct     float64  `json:"asterisk_cpu_pct"`
	PBXwareActiveCalls *float64 `json:"pbxware_active_calls,omitempty"`
}

// Failure is failed calls grouped by cause (largest count first).
type Failure struct {
	Cause        string `json:"cause"`
	SIPCode      int    `json:"sip_code,omitempty"`
	Count        int    `json:"count"`
	FirstAtCalls *int   `json:"first_at_calls,omitempty"`
	FirstAtS     *int   `json:"first_at_s,omitempty"`
}

// PBXwareView is the calls as PBXware itself saw them.
type PBXwareView struct {
	CDRStatus       map[string]map[string]int `json:"cdr_status,omitempty"`
	ActiveCallsPeak map[string]float64        `json:"active_calls_peak,omitempty"`
}

// ToolHealth says whether SwarmDialer, the load generator, held up.
type ToolHealth struct {
	SwarmDialerCPUPeakPct  float64                 `json:"swarmdialer_cpu_peak_pct"`
	UDPSendErrors          uint64                  `json:"udp_send_errors"`
	UDPReceiveErrors       uint64                  `json:"udp_receive_errors"`
	Extensions             int                     `json:"extensions"`
	NotRegisteredAtStart   int                     `json:"not_registered_at_start"`
	NotRegisteredAtEnd     int                     `json:"not_registered_at_end"`
	ReregistrationFailures uint64                  `json:"reregistration_failures"`
	MediaReceived          map[string]MediaQuality `json:"media_received,omitempty"`
}

type MediaQuality struct {
	Calls       uint64  `json:"calls"`
	LossPct     float64 `json:"loss_pct"`
	JitterMSAvg float64 `json:"jitter_ms_avg"`
	JitterMSMax float64 `json:"jitter_ms_max"`
}

// Event is one entry of a test's timeline.
type Event struct {
	TS      int      `json:"t_s"`
	Code    string   `json:"code"`
	Calls   int      `json:"calls"`
	Role    string   `json:"role,omitempty"`
	Metric  string   `json:"metric,omitempty"`
	Value   *float64 `json:"value,omitempty"`
	Cause   string   `json:"cause,omitempty"`
	SIPCode int      `json:"sip_code,omitempty"`
	Reason  string   `json:"reason,omitempty"`
	Count   int      `json:"count,omitempty"`
}

// HasDiagnostics reports whether the test carries SwarmDialer 1.6 diagnostics.
func (r *Result) HasDiagnostics() bool {
	return r.StopDetail != "" || r.QualityDegradedReason != "" || r.AtStop != nil || len(r.Failures) > 0 ||
		r.PBXware != nil || r.Tool != nil || len(r.Events) > 0
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
	N   int     `json:"n,omitempty"` // calls the MOS covers (diagnostics reports)
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

	// Per instance (MT, CC), diagnostics reports only.
	MP3ByInstance     map[string]MP3DelayN `json:"mp3_conversion_delay_s_by_instance,omitempty"`
	MissingRecordings map[string]int       `json:"missing_recordings,omitempty"`
}

type MP3DelayN struct {
	Avg   float64 `json:"avg"`
	P95   float64 `json:"p95"`
	Max   float64 `json:"max"`
	N     int     `json:"n"`
	Trend string  `json:"trend"`
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
	stopReasons = []string{"target_reached", "target_not_reached", "host_cpu_100", "host_ram_100", "vps_cpu_limit",
		"vps_ram_limit", "swarmdialer_overloaded", "cancelled", "error"}
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
	if r.Source != nil {
		if !identRe.MatchString(r.Source.Name) {
			return fail("source.name", "required: 1-64 characters of letters, digits, '.', '_' or '-'")
		}
		if strings.TrimSpace(r.Source.Version) == "" || len(r.Source.Version) > 64 {
			return fail("source.version", "required (max 64 characters)")
		}
	} else if strings.TrimSpace(r.SwarmDialerVersion) == "" {
		return fail("swarmdialer_version", "required unless source is given")
	}
	if len(r.SwarmDialerVersion) > 64 {
		return fail("swarmdialer_version", "max 64 characters")
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
	if r.IsHardware() {
		if len(r.Tests) > 0 {
			return fail("tests", "must be empty for profile %q", HardwareProfile)
		}
		if strings.TrimSpace(r.Environment.Host.CPUModel) == "" {
			return fail("environment.host.cpu_model", "required for profile %q", HardwareProfile)
		}
		return nil
	}
	if len(r.Tests) == 0 {
		return fail("tests", "at least one test is required (an empty list is only allowed for profile %q)", HardwareProfile)
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

func (r *Report) IsHardware() bool { return r.Profile.Name == HardwareProfile }

// DiagnosticsVersion is the first SwarmDialer version that sends diagnostics.
const DiagnosticsVersion = "1.6.0"

// HasDiagnostics reports whether a benchmark report uses the diagnostics
// format: sent by SwarmDialer 1.6.0 or later, or carrying its fields.
func (r *Report) HasDiagnostics() bool {
	if r.IsHardware() {
		return false
	}
	if r.Source == nil && VersionAtLeast(r.SwarmDialerVersion, DiagnosticsVersion) {
		return true
	}
	for i := range r.Tests {
		if r.Tests[i].Result.HasDiagnostics() {
			return true
		}
	}
	return false
}

// VersionAtLeast compares dotted numeric versions ("1.10.0" >= "1.6.0").
func VersionAtLeast(v, min string) bool {
	parse := func(s string) []int {
		var out []int
		for _, p := range strings.Split(strings.TrimPrefix(strings.TrimSpace(s), "v"), ".") {
			n := 0
			for _, c := range p {
				if c < '0' || c > '9' {
					break
				}
				n = n*10 + int(c-'0')
			}
			out = append(out, n)
		}
		return out
	}
	a, b := parse(v), parse(min)
	for i := 0; i < len(a) || i < len(b); i++ {
		x, y := 0, 0
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return x > y
		}
	}
	return true
}

// SourceName and SourceVersion identify the uploading tool.
func (r *Report) SourceName() string {
	if r.Source != nil {
		return r.Source.Name
	}
	return "swarmdialer"
}

func (r *Report) SourceVersion() string {
	if r.Source != nil {
		return r.Source.Version
	}
	return r.SwarmDialerVersion
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
	if len(h.Disks) > 256 || len(h.Network) > 256 || len(h.StorageControllers) > 256 || len(h.NICs) > 256 || len(h.Bonds) > 256 {
		return fail("environment.host", "too many disks, network interfaces, controllers, NICs or bonds")
	}
	for i, c := range h.StorageControllers {
		if c.Count < 0 {
			return fail(fmt.Sprintf("environment.host.storage_controllers[%d].count", i), "must not be negative")
		}
	}
	for i, n := range h.NICs {
		if n.Count < 0 || n.SpeedMbps < 0 {
			return fail(fmt.Sprintf("environment.host.nics[%d]", i), "count and speed_mbps must not be negative")
		}
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
	if len(res.Events) > 1000 || len(res.Failures) > 100 {
		return fail(path+".result", "too many events or failure groups")
	}
	for i, f := range res.Failures {
		if f.Count < 0 || strings.TrimSpace(f.Cause) == "" {
			return fail(fmt.Sprintf("%s.result.failures[%d]", path, i), "needs a cause and a count of 0 or more")
		}
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
