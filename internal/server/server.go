// Package server serves the upload API (/api/v1, upload keys only) and the
// admin web interface (session login only). The two never share credentials.
package server

import (
	"embed"
	"encoding/json"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"

	"dtcollector/internal/store"
)

//go:embed web
var webFS embed.FS

const (
	DefaultMaxUpload = 20 << 20
	sessionTTL       = 12 * time.Hour
)

type Config struct {
	Store *store.Store
	// Secure is true when served over HTTPS: Secure/__Host- cookies and HSTS.
	Secure         bool
	MaxUploadBytes int64
	Version        string
	Logger         *log.Logger
}

type Server struct {
	cfg      Config
	st       *store.Store
	log      *log.Logger
	pages    map[string]*template.Template
	assetVer string
	logins   *loginLimiter
	handler  http.Handler
}

func New(cfg Config) (*Server, error) {
	if cfg.MaxUploadBytes <= 0 {
		cfg.MaxUploadBytes = DefaultMaxUpload
	}
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	s := &Server{cfg: cfg, st: cfg.Store, log: cfg.Logger, logins: newLoginLimiter(10, 15*time.Minute)}
	if err := s.loadTemplates(); err != nil {
		return nil, err
	}

	api := http.NewServeMux()
	api.HandleFunc("GET /api/v1/ping", s.withUploadKey(s.handlePing))
	api.HandleFunc("POST /api/v1/reports", s.withUploadKey(s.handleUpload))
	api.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	})

	ui := http.NewServeMux()
	static, _ := fs.Sub(webFS, "web/static")
	ui.Handle("GET /static/", http.StripPrefix("/static/", staticHandler(http.FileServerFS(static))))
	ui.HandleFunc("GET /login", s.handleLoginPage)
	ui.HandleFunc("POST /login", s.handleLogin)
	ui.HandleFunc("POST /logout", s.handleLogout)

	ui.HandleFunc("GET /{$}", s.admin(s.handleReports))
	ui.HandleFunc("GET /reports/{id}", s.admin(s.handleReport))
	ui.HandleFunc("GET /reports/{id}/json", s.admin(s.handleReportJSON))
	ui.HandleFunc("POST /reports/{id}/note", s.admin(s.handleReportNote))
	ui.HandleFunc("POST /reports/{id}/delete", s.admin(s.handleReportDelete))
	ui.HandleFunc("GET /compare", s.admin(s.handleCompare))

	ui.HandleFunc("GET /keys", s.admin(s.handleKeys))
	ui.HandleFunc("POST /keys", s.admin(s.handleKeyCreate))
	ui.HandleFunc("POST /keys/{id}/revoke", s.admin(s.handleKeyRevoke))

	ui.HandleFunc("GET /admins", s.admin(s.handleAdmins))
	ui.HandleFunc("POST /admins", s.admin(s.handleAdminCreate))
	ui.HandleFunc("POST /admins/{id}/reset", s.admin(s.handleAdminReset))
	ui.HandleFunc("POST /admins/{id}/delete", s.admin(s.handleAdminDelete))
	ui.HandleFunc("GET /account", s.admin(s.handleAccount))
	ui.HandleFunc("POST /account/password", s.admin(s.handlePasswordChange))

	uiProtected := http.NewCrossOriginProtection().Handler(ui)

	s.handler = s.securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			api.ServeHTTP(w, r)
			return
		}
		uiProtected.ServeHTTP(w, r)
	}))
	return s, nil
}

func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		if s.cfg.Secure {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

func staticHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// PurgeLoop removes expired sessions periodically until stop is closed.
func (s *Server) PurgeLoop(stop <-chan struct{}) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if err := s.st.PurgeExpiredSessions(); err != nil {
				s.log.Printf("purge sessions: %v", err)
			}
			s.logins.purge()
		}
	}
}
