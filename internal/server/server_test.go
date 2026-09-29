package server

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"dtcollector/internal/auth"
	"dtcollector/internal/report"
	"dtcollector/internal/store"
)

type env struct {
	t      *testing.T
	st     *store.Store
	ts     *httptest.Server
	key    string
	keyID  int64
	client *http.Client // with cookie jar, logged in
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv, err := New(Config{Store: st, MaxUploadBytes: 2 << 20, Version: "test", Logger: log.New(io.Discard, "", 0)})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	key, prefix, hash := auth.NewUploadKey()
	id, err := st.CreateUploadKey("Test site", prefix, hash, "tester")
	if err != nil {
		t.Fatal(err)
	}
	h, _ := auth.HashPassword("correct horse battery")
	if _, err := st.CreateAdmin("admin", h); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, st: st, ts: ts, key: key, keyID: id}
	jar, _ := cookiejar.New(nil)
	e.client = &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp := e.form(e.client, "/login", url.Values{"username": {"admin"}, "password": {"correct horse battery"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	return e
}

func (e *env) form(c *http.Client, path string, v url.Values) *http.Response {
	e.t.Helper()
	req, _ := http.NewRequest("POST", e.ts.URL+path, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", e.ts.URL)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func (e *env) get(c *http.Client, path string) (*http.Response, string) {
	e.t.Helper()
	resp, err := c.Get(e.ts.URL + path)
	if err != nil {
		e.t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}

func (e *env) upload(key string, body []byte, gz bool) (int, map[string]any) {
	e.t.Helper()
	if gz {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		zw.Write(body)
		zw.Close()
		body = buf.Bytes()
	}
	req, _ := http.NewRequest("POST", e.ts.URL+"/api/v1/reports", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if gz {
		req.Header.Set("Content-Encoding", "gzip")
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func sample(seed uint64, host int) (*report.Report, []byte) {
	r := report.Sample(report.SampleHosts[host], seed, time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC))
	b, _ := json.Marshal(r)
	return r, b
}

func TestPing(t *testing.T) {
	e := newEnv(t)
	for _, c := range []struct {
		key  string
		want int
	}{{e.key, 200}, {"", 401}, {"dtk_wrong", 401}, {auth.UploadKeyPrefix + strings.Repeat("A", 43), 401}} {
		req, _ := http.NewRequest("GET", e.ts.URL+"/api/v1/ping", nil)
		if c.key != "" {
			req.Header.Set("Authorization", "Bearer "+c.key)
		}
		resp, _ := http.DefaultClient.Do(req)
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != c.want {
			t.Errorf("key %q: %d %s", c.key, resp.StatusCode, b)
		}
		if c.want == 200 && strings.TrimSpace(string(b)) != `{"status":"ok"}` {
			t.Errorf("body %s", b)
		}
	}
}

func TestUploadContract(t *testing.T) {
	e := newEnv(t)
	r, body := sample(1, 0)

	code, out := e.upload(e.key, body, false)
	if code != 201 || out["id"] != r.ReportID {
		t.Fatalf("first upload: %d %v", code, out)
	}
	code, out = e.upload(e.key, body, true)
	if code != 200 || out["duplicate"] != true || out["id"] != r.ReportID {
		t.Fatalf("duplicate: %d %v", code, out)
	}
	_, body2 := sample(2, 1)
	if code, out = e.upload(e.key, body2, true); code != 201 {
		t.Fatalf("gzip upload: %d %v", code, out)
	}
	code, out = e.upload(e.key, []byte(`{"schema_version":1,"report_id":"x"}`), false)
	if code != 400 || !strings.HasPrefix(out["error"].(string), "report_id:") {
		t.Fatalf("invalid: %d %v", code, out)
	}
	if code, _ = e.upload("", body, false); code != 401 {
		t.Fatalf("no key: %d", code)
	}

	big := append([]byte(`{"pad":"`), bytes.Repeat([]byte("a"), 3<<20)...)
	big = append(big, []byte(`"}`)...)
	if code, _ = e.upload(e.key, big, false); code != 413 {
		t.Fatalf("too large: %d", code)
	}
	if code, _ = e.upload(e.key, big, true); code != 413 { // small compressed, large decompressed
		t.Fatalf("gzip bomb: %d", code)
	}

	// Stored report comes back byte-identical.
	rows, _ := e.st.ListReports(store.ReportFilter{})
	if len(rows) != 2 {
		t.Fatalf("stored %d reports", len(rows))
	}
	raw, _ := e.st.ReportJSON(t.Context(), r.ReportID)
	if !bytes.Equal(raw, body) {
		t.Fatal("stored JSON differs from upload")
	}

	// A revoked key is refused.
	if err := e.st.RevokeUploadKey(e.keyID); err != nil {
		t.Fatal(err)
	}
	if code, _ = e.upload(e.key, body, false); code != 401 {
		t.Fatalf("revoked key: %d", code)
	}
}

func TestCredentialsDontCross(t *testing.T) {
	e := newEnv(t)
	// An upload key gets nowhere in the admin interface.
	for _, p := range []string{"/", "/keys", "/reports/00000000-0000-4000-a000-000000000000/json"} {
		req, _ := http.NewRequest("GET", e.ts.URL+p, nil)
		req.Header.Set("Authorization", "Bearer "+e.key)
		c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, _ := c.Do(req)
		resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Errorf("key on %s: %d", p, resp.StatusCode)
		}
	}
	// A session cookie gets nowhere in the API.
	resp, _ := e.get(e.client, "/api/v1/ping")
	if resp.StatusCode != 401 {
		t.Errorf("session on API: %d", resp.StatusCode)
	}
}

func TestCrossOriginPostRejected(t *testing.T) {
	e := newEnv(t)
	req, _ := http.NewRequest("POST", e.ts.URL+"/keys", strings.NewReader("name=evil"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site POST: %d", resp.StatusCode)
	}
}

func TestLoginRateLimit(t *testing.T) {
	e := newEnv(t)
	c := &http.Client{}
	var last int
	for i := 0; i < 11; i++ {
		last = e.form(c, "/login", url.Values{"username": {"admin"}, "password": {"wrong password!!"}}).StatusCode
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("after 11 failures: %d", last)
	}
}

func TestPages(t *testing.T) {
	e := newEnv(t)
	var ids []string
	for i := range 3 {
		r, b := sample(uint64(10+i), i)
		if code, out := e.upload(e.key, b, false); code != 201 {
			t.Fatalf("upload: %d %v", code, out)
		}
		ids = append(ids, r.ReportID)
	}
	checks := map[string][]string{
		"/":                                      {"Compare selected", "AMD EPYC 7443P", "Test site"},
		"/?q=EPYC":                               {"AMD EPYC 7443P"},
		"/?profile=standard:1&sw=5.2.1":          {"Xeon(R) Gold 6338"},
		"/reports/" + ids[0]:                     {"ramp_norec_low", "Environment", "pbxware_mt", "Target reached"},
		"/compare?ids=" + strings.Join(ids, ","): {"Compare 3 reports", "Max concurrent calls", "best", "MP3 conversion delay avg"},
		"/keys":                                  {"Test site", "Active", "API (Upload) Keys"},
		"/accounts":                              {"admin", "(you)", "Change password", "Create admin"},
	}
	for p, want := range checks {
		resp, body := e.get(e.client, p)
		if resp.StatusCode != 200 {
			t.Errorf("%s: %d", p, resp.StatusCode)
			continue
		}
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: missing %q", p, w)
			}
		}
	}
	if resp, body := e.get(e.client, "/?q=nothing-matches"); resp.StatusCode != 200 || !strings.Contains(body, "No reports match") {
		t.Errorf("empty filter: %d", resp.StatusCode)
	}
	if resp, _ := e.get(e.client, "/reports/"+ids[0]+"/json"); resp.Header.Get("Content-Type") != "application/json" {
		t.Errorf("json content type %q", resp.Header.Get("Content-Type"))
	}
	if resp, _ := e.get(e.client, "/reports/nope"); resp.StatusCode != 404 {
		t.Errorf("bad id: %d", resp.StatusCode)
	}
	if resp, _ := e.get(e.client, "/compare?ids="+ids[0]); resp.StatusCode != 400 {
		t.Errorf("compare one: %d", resp.StatusCode)
	}
}

func TestCompareRejectsMixedProfiles(t *testing.T) {
	e := newEnv(t)
	r1, b1 := sample(20, 0)
	r2 := report.Sample(report.SampleHosts[1], 21, time.Now())
	r2.Profile.Version = 2
	b2, _ := json.Marshal(r2)
	e.upload(e.key, b1, false)
	e.upload(e.key, b2, false)
	resp, body := e.get(e.client, "/compare?ids="+r1.ReportID+"&ids="+r2.ReportID)
	if resp.StatusCode != 400 || !strings.Contains(body, "one test profile version") {
		t.Fatalf("mixed profiles: %d", resp.StatusCode)
	}
}

func TestKeyAndAdminManagement(t *testing.T) {
	e := newEnv(t)
	req, _ := http.NewRequest("POST", e.ts.URL+"/keys", strings.NewReader("name=Chicago"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	i := strings.Index(string(b), `<code id="newkey">`)
	if resp.StatusCode != 200 || i < 0 {
		t.Fatalf("create key: %d", resp.StatusCode)
	}
	newKey := string(b[i+len(`<code id="newkey">`):])
	newKey = newKey[:strings.Index(newKey, "<")]
	if !auth.LooksLikeUploadKey(newKey) {
		t.Fatalf("new key %q", newKey)
	}
	_, body := sample(30, 2)
	if code, _ := e.upload(newKey, body, false); code != 201 {
		t.Fatalf("upload with new key: %d", code)
	}
	if _, page := e.get(e.client, "/keys"); strings.Contains(page, newKey) {
		t.Fatal("full key shown again on the keys page")
	}

	if r := e.form(e.client, "/accounts", url.Values{"username": {"second"}}); r.StatusCode != 200 {
		t.Fatalf("create admin: %d", r.StatusCode)
	}
	me, _ := e.st.AdminByUsername("admin")
	if r := e.form(e.client, "/accounts/"+itoa(me.ID)+"/delete", nil); r.StatusCode != 400 {
		t.Fatalf("delete self: %d", r.StatusCode)
	}
	// Changing the password ends the session.
	r := e.form(e.client, "/accounts/password", url.Values{"current": {"correct horse battery"}, "password": {"another long password"}, "confirm": {"another long password"}})
	if r.StatusCode != http.StatusSeeOther {
		t.Fatalf("change password: %d", r.StatusCode)
	}
	if resp, _ := e.get(e.client, "/keys"); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("session survived password change: %d", resp.StatusCode)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestHardwareReportsServer(t *testing.T) {
	e := newEnv(t)
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	var ids []string
	for i, script := range []bool{false, true} {
		r := report.SampleHardware(report.SampleHosts[i], uint64(40+i), now, script)
		b, _ := json.Marshal(r)
		if code, out := e.upload(e.key, b, true); code != 201 {
			t.Fatalf("hardware upload (script=%v): %d %v", script, code, out)
		}
		ids = append(ids, r.ReportID)
	}
	br, bb := sample(50, 2)
	e.upload(e.key, bb, false)

	_, body := e.get(e.client, "/?kind=hardware")
	if !strings.Contains(body, ids[0]) || !strings.Contains(body, ids[1]) || strings.Contains(body, br.ReportID) {
		t.Error("kind=hardware filter")
	}
	if !strings.Contains(body, "Hardware only") || !strings.Contains(body, "hw-collect 1.0.0") || !strings.Contains(body, "Supermicro SYS-6029BT-DNC0R") {
		t.Error("hardware list row content")
	}
	_, body = e.get(e.client, "/?kind=benchmark")
	if strings.Contains(body, ids[0]) || !strings.Contains(body, br.ReportID) {
		t.Error("kind=benchmark filter")
	}
	resp, body := e.get(e.client, "/reports/"+ids[1])
	if resp.StatusCode != 200 {
		t.Fatalf("hardware report page: %d", resp.StatusCode)
	}
	for _, w := range []string{"hardware-only report", "X11DPT-B", "SAS3008", "Ethernet Controller X550", "Not included in this report."} {
		if !strings.Contains(body, w) {
			t.Errorf("hardware report page missing %q", w)
		}
	}
	if strings.Contains(body, "report-charts") {
		t.Error("hardware report page has a charts section")
	}
	if resp, _ := e.get(e.client, "/compare?ids="+ids[0]+","+ids[1]); resp.StatusCode != 400 {
		t.Errorf("compare hardware: %d", resp.StatusCode)
	}
}

func TestOldAccountURLsRedirect(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/admins", "/account"} {
		resp, _ := e.get(e.client, p)
		if resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Location") != "/accounts" {
			t.Errorf("%s: %d %q", p, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
}
