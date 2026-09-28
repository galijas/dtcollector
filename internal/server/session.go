package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"dtcollector/internal/auth"
	"dtcollector/internal/store"
)

func (s *Server) cookieName() string {
	if s.cfg.Secure {
		return "__Host-dtc_session"
	}
	return "dtc_session"
}

func (s *Server) currentAdmin(r *http.Request) *store.Admin {
	c, err := r.Cookie(s.cookieName())
	if err != nil || c.Value == "" {
		return nil
	}
	a, err := s.st.SessionAdmin(auth.HashToken(c.Value))
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.log.Printf("session lookup: %v", err)
		}
		return nil
	}
	return a
}

// admin requires a logged-in admin session.
func (s *Server) admin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := s.currentAdmin(r)
		if a == nil {
			if r.Method == http.MethodGet {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
			} else {
				http.Error(w, "not logged in", http.StatusUnauthorized)
			}
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		next(w, r.WithContext(context.WithValue(r.Context(), adminCtx, a)))
	}
}

func adminFrom(r *http.Request) *store.Admin {
	a, _ := r.Context().Value(adminCtx).(*store.Admin)
	return a
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if s.currentAdmin(r) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, r, http.StatusOK, "login.html", "Log in", nil)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	username := strings.TrimSpace(r.PostFormValue("username"))
	password := r.PostFormValue("password")
	fail := func(code int, msg string) {
		w.Header().Set("Cache-Control", "no-store")
		s.render(w, r, code, "login.html", "Log in", map[string]string{"Error": msg, "Username": username})
	}
	if !s.logins.allowed(ip) {
		s.log.Printf("login: rate limited %s", ip)
		fail(http.StatusTooManyRequests, "Too many failed attempts. Try again in a few minutes.")
		return
	}
	a, err := s.st.AdminByUsername(username)
	hash := ""
	if err == nil {
		hash = a.PasswordHash
	}
	if !auth.CheckPassword(hash, password) {
		s.logins.failed(ip)
		s.log.Printf("login: failed for %q from %s", username, ip)
		fail(http.StatusUnauthorized, "Wrong username or password.")
		return
	}
	s.logins.reset(ip)
	token := auth.NewSessionToken()
	if err := s.st.CreateSession(auth.HashToken(token), a.ID, sessionTTL); err != nil {
		s.log.Printf("login: create session: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.st.TouchAdminLogin(a.ID)
	s.log.Printf("login: %q from %s", a.Username, ip)
	http.SetCookie(w, &http.Cookie{
		Name: s.cookieName(), Value: token, Path: "/",
		MaxAge: int(sessionTTL.Seconds()), HttpOnly: true, Secure: s.cfg.Secure, SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(s.cookieName()); err == nil {
		s.st.DeleteSession(auth.HashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.cfg.Secure, SameSite: http.SameSiteStrictMode})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// The server is reached directly (no reverse proxy), so RemoteAddr is the client.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type loginLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	m      map[string]*attempts
}

type attempts struct {
	n     int
	first time.Time
}

func newLoginLimiter(max int, window time.Duration) *loginLimiter {
	return &loginLimiter{max: max, window: window, m: map[string]*attempts{}}
}

func (l *loginLimiter) allowed(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.m[ip]
	if a == nil || time.Since(a.first) > l.window {
		return true
	}
	return a.n < l.max
}

func (l *loginLimiter) failed(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.m[ip]
	if a == nil || time.Since(a.first) > l.window {
		l.m[ip] = &attempts{1, time.Now()}
		return
	}
	a.n++
}

func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	delete(l.m, ip)
	l.mu.Unlock()
}

func (l *loginLimiter) purge() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for ip, a := range l.m {
		if time.Since(a.first) > l.window {
			delete(l.m, ip)
		}
	}
}
