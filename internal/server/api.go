package server

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"dtcollector/internal/auth"
	"dtcollector/internal/report"
	"dtcollector/internal/store"
)

type ctxKey int

const (
	keyCtx ctxKey = iota
	adminCtx
)

func (s *Server) withUploadKey(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := bearer(r)
		var k *store.UploadKey
		if auth.LooksLikeUploadKey(key) {
			var err error
			k, err = s.st.ActiveUploadKey(auth.HashToken(key))
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				s.log.Printf("api: key lookup: %v", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
				return
			}
		}
		if k == nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="dtcollector"`)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing, invalid or revoked upload key"})
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), keyCtx, k)))
	}
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	k := r.Context().Value(keyCtx).(*store.UploadKey)
	max := s.cfg.MaxUploadBytes
	tooLarge := func() {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "report too large", "max_bytes": max})
	}

	body := http.MaxBytesReader(w, r.Body, max)
	var rd io.Reader = body
	switch enc := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))); enc {
	case "", "identity":
	case "gzip":
		zr, err := gzip.NewReader(body)
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				tooLarge()
				return
			}
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body: invalid gzip data"})
			return
		}
		rd = zr
	default:
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Encoding must be gzip or absent"})
		return
	}
	// The limit applies to the decompressed size too.
	raw, err := io.ReadAll(io.LimitReader(rd, max+1))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			tooLarge()
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body: " + err.Error()})
		return
	}
	if int64(len(raw)) > max {
		tooLarge()
		return
	}

	rep, err := report.Parse(raw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	sum := rep.Summary()
	inserted, err := s.st.InsertReport(sum, raw, k.ID)
	if err != nil {
		s.log.Printf("api: store report %s: %v", sum.ReportID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if !inserted {
		s.log.Printf("api: duplicate report %s from key %q", sum.ReportID, k.Name)
		writeJSON(w, http.StatusOK, map[string]any{"id": sum.ReportID, "duplicate": true})
		return
	}
	s.log.Printf("api: stored report %s (%s v%d, %d tests, %d bytes) from key %q",
		sum.ReportID, sum.ProfileName, sum.ProfileVersion, sum.TestCount, len(raw), k.Name)
	writeJSON(w, http.StatusCreated, map[string]string{"id": sum.ReportID})
}
