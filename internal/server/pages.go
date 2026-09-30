package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"dtcollector/internal/auth"
	"dtcollector/internal/report"
	"dtcollector/internal/store"
)

type pageData struct {
	Title   string
	Admin   *store.Admin
	Flash   string
	Version string
	Nav     string
	Asset   string
	Data    any
}

var flashes = map[string]string{
	"note":         "Note saved.",
	"deleted":      "Report deleted.",
	"revoked":      "API key revoked. Anything using it can no longer upload.",
	"admindeleted": "Admin account deleted.",
	"pwchanged":    "Password changed. Log in with the new password.",
	"hwadded":      "Hardware added to the list.",
	"hwsaved":      "Hardware entry saved.",
	"hwdeleted":    "Hardware entry deleted.",
}

var funcs = template.FuncMap{
	"ts": func(t time.Time) string {
		if t.IsZero() {
			return "never"
		}
		return t.UTC().Format("2006-01-02 15:04 UTC")
	},
	"tsStr": func(s string) string {
		t, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return s
		}
		return t.UTC().Format("2006-01-02 15:04:05 UTC")
	},
	"date": func(t time.Time) string { return t.UTC().Format("2006-01-02") },
	"gib":  func(n int64) string { return report.HumanBytes(n, 1024) },
	"gb":   func(n int64) string { return report.HumanBytes(n, 1000) },
	"num":  fmtNum,
	"pct":  func(v float64) string { return fmtNum(v) + "%" },
	"stop": stopLabel,
	"stopClass": func(s string) string {
		if s == "target_reached" {
			return "ok"
		}
		return "warn"
	},
	"testTitle": testTitle,
	"add":       func(a, b int) int { return a + b },
	"deref": func(p any) any {
		switch v := p.(type) {
		case *int:
			if v != nil {
				return *v
			}
		case *float64:
			if v != nil {
				return *v
			}
		case *string:
			if v != nil {
				return *v
			}
		}
		return nil
	},
}

func fmtNum(v float64) string {
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return strconv.FormatInt(int64(v), 10)
	}
	if math.Abs(v) >= 100 {
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
	if math.Abs(v) >= 10 {
		return strconv.FormatFloat(v, 'f', 1, 64)
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}

var stopLabels = map[string]string{
	"target_reached":         "Target reached",
	"host_cpu_100":           "Host CPU 100%",
	"host_ram_100":           "Host RAM 100%",
	"vps_cpu_limit":          "VPS CPU limit",
	"vps_ram_limit":          "VPS RAM limit",
	"swarmdialer_overloaded": "SwarmDialer overloaded",
	"error":                  "Error",
}

func stopLabel(s string) string {
	if l, ok := stopLabels[s]; ok {
		return l
	}
	return s
}

func testTitle(t report.Test) string {
	rec := map[string]string{"off": "No recording", "mono": "Mono recording", "stereo": "Stereo recording"}[t.Recording]
	if rec == "" {
		rec = t.Recording
	}
	codec := t.Codec.Caller
	if t.Codec.Callee != t.Codec.Caller {
		codec += " → " + t.Codec.Callee
	}
	mode := "Ramp"
	if t.Mode == "rolling" {
		mode = "Rolling"
	}
	return fmt.Sprintf("%s · %s · %s", mode, rec, codec)
}

func (s *Server) loadTemplates() error {
	// Static URLs carry a hash of the embedded files so browsers pick up a new build.
	h := sha256.New()
	fs.WalkDir(webFS, "web/static", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := webFS.ReadFile(p)
			h.Write(b)
		}
		return nil
	})
	s.assetVer = hex.EncodeToString(h.Sum(nil))[:10]
	s.pages = map[string]*template.Template{}
	for _, p := range []string{"login.html", "reports.html", "report.html", "compare.html", "keys.html", "accounts.html", "error.html", "hardware.html"} {
		t, err := template.New("").Funcs(funcs).ParseFS(webFS, "web/templates/base.html", "web/templates/"+p)
		if err != nil {
			return fmt.Errorf("template %s: %w", p, err)
		}
		s.pages[p] = t
	}
	return nil
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, code int, page, title string, data any) {
	pd := pageData{Title: title, Admin: adminFrom(r), Flash: flashes[r.URL.Query().Get("m")], Version: s.cfg.Version,
		Nav: strings.TrimSuffix(page, ".html"), Asset: s.assetVer, Data: data}
	var buf bytes.Buffer
	if err := s.pages[page].ExecuteTemplate(&buf, "base", pd); err != nil {
		s.log.Printf("render %s: %v", page, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	w.Write(buf.Bytes())
}

func (s *Server) errorPage(w http.ResponseWriter, r *http.Request, code int, msg string) {
	s.render(w, r, code, "error.html", http.StatusText(code), msg)
}

func (s *Server) internalError(w http.ResponseWriter, r *http.Request, what string, err error) {
	s.log.Printf("%s: %v", what, err)
	s.errorPage(w, r, http.StatusInternalServerError, "Something went wrong. The server log has details.")
}

// ---- reports ----

type reportsData struct {
	Rows      []store.ReportRow
	Opts      *store.FilterOptions
	Keys      []store.UploadKey
	Q         map[string]string
	Filtered  bool
	Truncated bool
	Limit     int
}

func (s *Server) handleReports(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	d := reportsData{Q: map[string]string{}, Limit: 500}
	for _, k := range []string{"kind", "profile", "sw", "pbx", "key", "q", "from", "to"} {
		d.Q[k] = strings.TrimSpace(q.Get(k))
		if d.Q[k] != "" {
			d.Filtered = true
		}
	}
	f := store.ReportFilter{Serverware: d.Q["sw"], PBXware: d.Q["pbx"], Text: d.Q["q"], Limit: d.Limit + 1}
	if k := d.Q["kind"]; k == report.KindBenchmark || k == report.KindHardware {
		f.Kind = k
	}
	if p := d.Q["profile"]; p != "" {
		name, ver, _ := strings.Cut(p, ":")
		f.ProfileName = name
		f.ProfileVersion, _ = strconv.Atoi(ver)
	}
	f.KeyID, _ = strconv.ParseInt(d.Q["key"], 10, 64)
	if t, err := time.Parse("2006-01-02", d.Q["from"]); err == nil {
		f.From = t
	}
	if t, err := time.Parse("2006-01-02", d.Q["to"]); err == nil {
		f.To = t.AddDate(0, 0, 1)
	}
	var err error
	if d.Rows, err = s.st.ListReports(f); err != nil {
		s.internalError(w, r, "list reports", err)
		return
	}
	if len(d.Rows) > d.Limit {
		d.Rows, d.Truncated = d.Rows[:d.Limit], true
	}
	if d.Opts, err = s.st.FilterOptions(); err != nil {
		s.internalError(w, r, "filter options", err)
		return
	}
	if d.Keys, err = s.st.ListUploadKeys(); err != nil {
		s.internalError(w, r, "list keys", err)
		return
	}
	s.render(w, r, http.StatusOK, "reports.html", "Reports", d)
}

var reportIDRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (s *Server) loadReport(w http.ResponseWriter, r *http.Request, id string) (*store.ReportRow, *report.Report, bool) {
	id = strings.ToLower(id)
	if !reportIDRe.MatchString(id) {
		s.errorPage(w, r, http.StatusNotFound, "No report with that ID.")
		return nil, nil, false
	}
	meta, err := s.st.GetReport(id)
	if errors.Is(err, store.ErrNotFound) {
		s.errorPage(w, r, http.StatusNotFound, "No report with that ID.")
		return nil, nil, false
	}
	if err != nil {
		s.internalError(w, r, "get report", err)
		return nil, nil, false
	}
	raw, err := s.st.ReportJSON(r.Context(), id)
	if err != nil {
		s.internalError(w, r, "report json", err)
		return nil, nil, false
	}
	var rep report.Report
	if err := json.Unmarshal(raw, &rep); err != nil {
		s.internalError(w, r, "decode stored report "+id, err)
		return nil, nil, false
	}
	return meta, &rep, true
}

type reportData struct {
	Meta     *store.ReportRow
	R        *report.Report
	VPSNames []string
}

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	meta, rep, ok := s.loadReport(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	d := reportData{Meta: meta, R: rep}
	for n := range rep.Environment.VPS {
		d.VPSNames = append(d.VPSNames, n)
	}
	sort.Strings(d.VPSNames)
	s.render(w, r, http.StatusOK, "report.html", "Report "+meta.CreatedAt.UTC().Format("2006-01-02 15:04"), d)
}

func (s *Server) handleReportJSON(w http.ResponseWriter, r *http.Request) {
	id := strings.ToLower(r.PathValue("id"))
	if !reportIDRe.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	raw, err := s.st.ReportJSON(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.log.Printf("report json %s: %v", id, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", `attachment; filename="dtcollector-report-`+id+`.json"`)
	}
	w.Write(raw)
}

func (s *Server) handleReportNote(w http.ResponseWriter, r *http.Request) {
	id := strings.ToLower(r.PathValue("id"))
	note := strings.TrimSpace(r.PostFormValue("note"))
	if len([]rune(note)) > 500 {
		note = string([]rune(note)[:500])
	}
	if err := s.st.SetReportNote(id, note); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.errorPage(w, r, http.StatusNotFound, "No report with that ID.")
			return
		}
		s.internalError(w, r, "set note", err)
		return
	}
	http.Redirect(w, r, "/reports/"+id+"?m=note", http.StatusSeeOther)
}

func (s *Server) handleReportDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.ToLower(r.PathValue("id"))
	if err := s.st.DeleteReport(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.errorPage(w, r, http.StatusNotFound, "No report with that ID.")
			return
		}
		s.internalError(w, r, "delete report", err)
		return
	}
	s.log.Printf("report %s deleted by %q", id, adminFrom(r).Username)
	http.Redirect(w, r, "/reports?m=deleted", http.StatusSeeOther)
}

// ---- upload keys ----

type keysData struct {
	Keys    []store.UploadKey
	NewKey  string
	NewName string
	Error   string
}

func (s *Server) renderKeys(w http.ResponseWriter, r *http.Request, code int, d keysData) {
	keys, err := s.st.ListUploadKeys()
	if err != nil {
		s.internalError(w, r, "list keys", err)
		return
	}
	d.Keys = keys
	s.render(w, r, code, "keys.html", "API (Upload) Keys", d)
}

func (s *Server) handleKeys(w http.ResponseWriter, r *http.Request) {
	s.renderKeys(w, r, http.StatusOK, keysData{})
}

func (s *Server) handleKeyCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PostFormValue("name"))
	if name == "" || len([]rune(name)) > 80 {
		s.renderKeys(w, r, http.StatusBadRequest, keysData{Error: "Give the API key a name of 1 to 80 characters (for example the site or network it is for)."})
		return
	}
	key, prefix, hash := auth.NewUploadKey()
	if _, err := s.st.CreateUploadKey(name, prefix, hash, adminFrom(r).Username); err != nil {
		s.internalError(w, r, "create key", err)
		return
	}
	s.log.Printf("API key %q (%s…) created by %q", name, prefix, adminFrom(r).Username)
	s.renderKeys(w, r, http.StatusOK, keysData{NewKey: key, NewName: name})
}

func (s *Server) handleKeyRevoke(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.st.RevokeUploadKey(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.errorPage(w, r, http.StatusNotFound, "No active API key with that ID.")
			return
		}
		s.internalError(w, r, "revoke key", err)
		return
	}
	s.log.Printf("API key %d revoked by %q", id, adminFrom(r).Username)
	http.Redirect(w, r, "/keys?m=revoked", http.StatusSeeOther)
}

// ---- admin accounts ----

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{1,31}$`)

type adminsData struct {
	Admins      []store.Admin
	NewUser     string
	NewPassword string
	Reset       bool
	Error       string
	PWError     string
}

func (s *Server) renderAdmins(w http.ResponseWriter, r *http.Request, code int, d adminsData) {
	admins, err := s.st.ListAdmins()
	if err != nil {
		s.internalError(w, r, "list admins", err)
		return
	}
	d.Admins = admins
	s.render(w, r, code, "accounts.html", "Accounts", d)
}

func (s *Server) handleAccounts(w http.ResponseWriter, r *http.Request) {
	s.renderAdmins(w, r, http.StatusOK, adminsData{})
}

func (s *Server) handleAdminCreate(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.PostFormValue("username"))
	if !usernameRe.MatchString(username) {
		s.renderAdmins(w, r, http.StatusBadRequest, adminsData{Error: "Usernames are 2 to 32 characters: letters, digits, '.', '_' or '-'."})
		return
	}
	pw := auth.GeneratePassword()
	hash, err := auth.HashPassword(pw)
	if err != nil {
		s.internalError(w, r, "hash password", err)
		return
	}
	if _, err := s.st.CreateAdmin(username, hash); err != nil {
		if errors.Is(err, store.ErrExists) {
			s.renderAdmins(w, r, http.StatusConflict, adminsData{Error: "An admin named " + username + " already exists."})
			return
		}
		s.internalError(w, r, "create admin", err)
		return
	}
	s.log.Printf("admin %q created by %q", username, adminFrom(r).Username)
	s.renderAdmins(w, r, http.StatusOK, adminsData{NewUser: username, NewPassword: pw})
}

func (s *Server) handleAdminReset(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	me := adminFrom(r)
	if id == me.ID {
		s.renderAdmins(w, r, http.StatusBadRequest, adminsData{Error: "Change your own password in the Change password section above."})
		return
	}
	target, err := s.st.AdminByID(id)
	if errors.Is(err, store.ErrNotFound) {
		s.errorPage(w, r, http.StatusNotFound, "No admin with that ID.")
		return
	}
	if err != nil {
		s.internalError(w, r, "get admin", err)
		return
	}
	pw := auth.GeneratePassword()
	hash, err := auth.HashPassword(pw)
	if err == nil {
		err = s.st.SetAdminPassword(id, hash)
	}
	if err != nil {
		s.internalError(w, r, "reset password", err)
		return
	}
	s.log.Printf("password of admin %q reset by %q", target.Username, me.Username)
	s.renderAdmins(w, r, http.StatusOK, adminsData{NewUser: target.Username, NewPassword: pw, Reset: true})
}

func (s *Server) handleAdminDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	me := adminFrom(r)
	if id == me.ID {
		s.renderAdmins(w, r, http.StatusBadRequest, adminsData{Error: "You can't delete your own account. Log in as another admin to do that."})
		return
	}
	err := s.st.DeleteAdmin(id)
	switch {
	case errors.Is(err, store.ErrLastAdmin):
		s.renderAdmins(w, r, http.StatusBadRequest, adminsData{Error: "The last admin account can't be deleted."})
		return
	case errors.Is(err, store.ErrNotFound):
		s.errorPage(w, r, http.StatusNotFound, "No admin with that ID.")
		return
	case err != nil:
		s.internalError(w, r, "delete admin", err)
		return
	}
	s.log.Printf("admin %d deleted by %q", id, me.Username)
	http.Redirect(w, r, "/accounts?m=admindeleted", http.StatusSeeOther)
}

func (s *Server) handlePasswordChange(w http.ResponseWriter, r *http.Request) {
	me := adminFrom(r)
	cur, pw, confirm := r.PostFormValue("current"), r.PostFormValue("password"), r.PostFormValue("confirm")
	fail := func(msg string) {
		s.renderAdmins(w, r, http.StatusBadRequest, adminsData{PWError: msg})
	}
	if !auth.CheckPassword(me.PasswordHash, cur) {
		fail("The current password is wrong.")
		return
	}
	if pw != confirm {
		fail("The new passwords don't match.")
		return
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		fail(strings.ToUpper(err.Error()[:1]) + err.Error()[1:] + ".")
		return
	}
	if err := s.st.SetAdminPassword(me.ID, hash); err != nil {
		s.internalError(w, r, "change password", err)
		return
	}
	s.log.Printf("admin %q changed their password", me.Username)
	http.Redirect(w, r, "/login?m=pwchanged", http.StatusSeeOther)
}
