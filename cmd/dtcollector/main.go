// Command dtcollector is the DT Collector server and its admin CLI.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/acme/autocert"

	"dtcollector/internal/auth"
	"dtcollector/internal/hardware"
	"dtcollector/internal/server"
	"dtcollector/internal/store"
)

var version = "dev"

const usage = `DT Collector %s

Usage:
  dtcollector serve -domain <dns name> -email <address> [-data-dir DIR]
  dtcollector serve -dev-addr 127.0.0.1:8080 [-data-dir DIR]   (plain HTTP, local testing only)
  dtcollector create-admin -username NAME [-data-dir DIR]      (prints a generated password)
  dtcollector reset-password -username NAME [-data-dir DIR]    (prints a generated password)
  dtcollector backup [-data-dir DIR] [-dest DIR] [-keep N]
  dtcollector export-hardware [-data-dir DIR] [-out FILE]      (HW Validation list as a starter list for new installs)
  dtcollector version
`

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, usage, version)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = cmdServe(os.Args[2:])
	case "create-admin":
		err = cmdAdmin(os.Args[2:], false)
	case "reset-password":
		err = cmdAdmin(os.Args[2:], true)
	case "backup":
		err = cmdBackup(os.Args[2:])
	case "export-hardware":
		err = cmdExportHardware(os.Args[2:])
	case "version", "-version", "--version":
		fmt.Println(version)
	default:
		fmt.Fprintf(os.Stderr, usage, version)
		os.Exit(2)
	}
	if err != nil {
		log.Fatalf("dtcollector: %v", err)
	}
}

const defaultDataDir = "/var/lib/dtcollector"

func openStore(dataDir string) (*store.Store, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	return store.Open(filepath.Join(dataDir, "dtcollector.db"))
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dataDir := fs.String("data-dir", defaultDataDir, "directory for the database and certificates")
	domain := fs.String("domain", "", "public DNS name of this server (Let's Encrypt certificate)")
	email := fs.String("email", "", "contact email for Let's Encrypt")
	httpsAddr := fs.String("https-addr", ":443", "HTTPS listen address")
	httpAddr := fs.String("http-addr", ":80", "HTTP listen address (ACME challenges and redirect to HTTPS)")
	devAddr := fs.String("dev-addr", "", "serve plain HTTP on this address instead, without TLS (local testing only)")
	maxUpload := fs.Int64("max-upload-mb", 20, "maximum report size in MB (compressed and decompressed)")
	fs.Parse(args)

	if *devAddr == "" && (*domain == "" || *email == "") {
		return errors.New("serve needs -domain and -email (or -dev-addr for local testing)")
	}
	logger := log.New(os.Stderr, "", 0)
	if os.Getenv("INVOCATION_ID") == "" { // not under systemd, which timestamps itself
		logger.SetFlags(log.LstdFlags)
	}

	st, err := openStore(*dataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	srv, err := server.New(server.Config{
		Store: st, Secure: *devAddr == "", MaxUploadBytes: *maxUpload << 20, Version: version, Logger: logger,
	})
	if err != nil {
		return err
	}
	stop := make(chan struct{})
	go srv.PurgeLoop(stop)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	var servers []*http.Server
	errc := make(chan error, 2)

	if *devAddr != "" {
		logger.Printf("WARNING: serving plain HTTP on %s (dev mode; never expose this)", *devAddr)
		hs := newHTTPServer(*devAddr, srv.Handler(), logger)
		servers = append(servers, hs)
		go func() { errc <- hs.ListenAndServe() }()
	} else {
		host := strings.ToLower(strings.TrimSuffix(*domain, "."))
		m := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(host),
			Cache:      autocert.DirCache(filepath.Join(*dataDir, "autocert")),
			Email:      *email,
		}
		tlsCfg := m.TLSConfig()
		tlsCfg.MinVersion = tls.VersionTLS12
		hs := newHTTPServer(*httpsAddr, srv.Handler(), logger)
		hs.TLSConfig = tlsCfg
		redirect := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://"+host+r.URL.RequestURI(), http.StatusMovedPermanently)
		})
		plain := newHTTPServer(*httpAddr, m.HTTPHandler(redirect), logger)
		plain.ReadTimeout, plain.WriteTimeout = 10*time.Second, 10*time.Second
		servers = append(servers, hs, plain)
		logger.Printf("serving https://%s/ on %s (certificates in %s), HTTP on %s", host, *httpsAddr,
			filepath.Join(*dataDir, "autocert"), *httpAddr)
		go func() { errc <- hs.ListenAndServeTLS("", "") }()
		go func() { errc <- plain.ListenAndServe() }()
	}

	select {
	case err = <-errc:
	case <-ctx.Done():
		logger.Printf("shutting down")
	}
	close(stop)
	sctx, scancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer scancel()
	for _, s := range servers {
		s.Shutdown(sctx)
	}
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

func newHTTPServer(addr string, h http.Handler, logger *log.Logger) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute, // a 20 MB upload over a slow link
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          log.New(tlsNoiseFilter{logger}, "", 0),
	}
}

// tlsNoiseFilter drops the handshake errors every public HTTPS server gets
// from scanners (unknown SNI, bare-IP connections), keeping the log useful.
type tlsNoiseFilter struct{ l *log.Logger }

func (f tlsNoiseFilter) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "TLS handshake error") {
		return len(p), nil
	}
	f.l.Print(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

func cmdAdmin(args []string, reset bool) error {
	name := "create-admin"
	if reset {
		name = "reset-password"
	}
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	dataDir := fs.String("data-dir", defaultDataDir, "data directory")
	username := fs.String("username", "", "admin username")
	fs.Parse(args)
	if *username == "" {
		return errors.New(name + " needs -username")
	}
	st, err := openStore(*dataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	pw := auth.GeneratePassword()
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	if reset {
		a, err := st.AdminByUsername(*username)
		if err != nil {
			return fmt.Errorf("admin %q: %w", *username, err)
		}
		if err := st.SetAdminPassword(a.ID, hash); err != nil {
			return err
		}
	} else if _, err := st.CreateAdmin(*username, hash); err != nil {
		return fmt.Errorf("create admin %q: %w", *username, err)
	}
	fmt.Printf("Username: %s\nPassword: %s\n", *username, pw)
	return nil
}

func cmdBackup(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	dataDir := fs.String("data-dir", defaultDataDir, "data directory")
	dest := fs.String("dest", "", "backup directory (default <data-dir>/backups)")
	keep := fs.Int("keep", 14, "number of backups to keep")
	fs.Parse(args)
	if *dest == "" {
		*dest = filepath.Join(*dataDir, "backups")
	}
	if err := os.MkdirAll(*dest, 0o700); err != nil {
		return err
	}
	st, err := openStore(*dataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	file := filepath.Join(*dest, "dtcollector-"+time.Now().UTC().Format("20060102-150405")+".db")
	if err := st.Backup(file); err != nil {
		return fmt.Errorf("backup to %s: %w", file, err)
	}
	fmt.Println("backup written:", file)

	old, _ := filepath.Glob(filepath.Join(*dest, "dtcollector-*.db"))
	sort.Strings(old)
	for len(old) > *keep {
		if err := os.Remove(old[0]); err != nil {
			return err
		}
		fmt.Println("removed old backup:", old[0])
		old = old[1:]
	}
	return nil
}

// cmdExportHardware writes the HW Validation list in the starter-list format
// (internal/hardware/seed/hardware-list.json), so new installations begin
// with the live list.
func cmdExportHardware(args []string) error {
	fs := flag.NewFlagSet("export-hardware", flag.ExitOnError)
	dataDir := fs.String("data-dir", defaultDataDir, "data directory")
	out := fs.String("out", "", "output file (default: standard output)")
	fs.Parse(args)
	st, err := openStore(*dataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	rows, err := st.ListParts()
	if err != nil {
		return err
	}
	parts := make([]hardware.Part, len(rows))
	for i, r := range rows {
		parts[i] = r.Part
	}
	b, err := json.MarshalIndent(hardware.NewSnapshot(parts), "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if *out == "" {
		_, err = os.Stdout.Write(b)
		return err
	}
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "exported %d parts to %s\n", len(parts), *out)
	return nil
}
