# DT Collector: project state

Read this first when continuing work. The original brief is
`DTcollector_project.md`; the upload contract is `docs/api.md`.

## Status (2026-09-28)

First version built, tested locally, not yet deployed.

Done:
- Upload API: `GET /api/v1/ping`, `POST /api/v1/reports` (gzip, 20 MB
  limit on compressed and decompressed size, idempotent on `report_id`,
  field-specific 400 messages).
- Report format v1 validation (`internal/report`), lenient on unknown
  fields and on the "(tbc)" hardware fields.
- SQLite store: reports as gzipped raw JSON plus indexed summary columns;
  admins, sessions, hashed upload keys.
- Web interface: login, report list with filters (profile, SERVERware,
  PBXware, source key, date range, text search), report page (environment,
  results table, 10 charts per test), compare page (2 to 8 reports of one
  profile version: per-test table with best values marked, overlaid
  charts), upload key management, admin accounts, own password change,
  per-report note, JSON download, report deletion.
- `install.sh`: DNS name and email prompts, Go install, build, system
  user, sandboxed systemd unit, daily backup timer, ufw, first admin,
  certificate check.
- Tests: `go test ./...` covers the API contract (all status codes, gzip
  bomb, duplicate, revoked key), keys and sessions not crossing over,
  cross-origin POST rejection, login rate limit, every page rendering, key
  and admin management.

## Decisions

- Single Go binary, `modernc.org/sqlite` (pure Go, no cgo), Let's Encrypt
  through `golang.org/x/crypto/acme/autocert` (no certbot). Certificates in
  `/var/lib/dtcollector/autocert`.
- Upload keys: `dtk_` + 43 chars `[A-Za-z0-9]`, stored as SHA-256 (random
  keys don't need a slow hash). Display prefix is the first 10 characters.
- Admin passwords: bcrypt cost 12, minimum 12 characters. New admins and
  resets get a generated password shown once.
- CSRF: Go's `http.CrossOriginProtection` plus `SameSite=Strict` cookies,
  no tokens.
- Stored report JSON is exactly what was uploaded (unknown fields kept).
- Compare only within one `profile.name` + `profile.version` (server
  enforces; the list page also blocks mixed selections).
- "Best" marking in compare skips Asterisk CPU % (it scales with core count).
- Charts: uPlot 1.6.32, bundled in `internal/server/web/static/vendor`
  (no CDN). Colors are the validated categorical palette, light and dark.

## Open items

- Units of `host_net_*_bps` (assumed bits/s) and `host_disk_*_bps`
  (assumed bytes/s): confirm when building SwarmDialer's side.
- The "(tbc)" host fields (CPU model, disks, disk type): confirm what
  SwarmDialer can read from SERVERware's Prometheus; the server accepts
  them missing.
- Not deployed yet: needs the DNS name and a server.
- GitHub repo `galijas/dtcollector` to be created, then push `main`.

## Development notes

- Local run without TLS: `dtcollector serve -dev-addr 127.0.0.1:8080 -data-dir ./data`.
- `go run ./cmd/dtc-sample` prints a complete synthetic report (three
  synthetic hosts via `-host 0..2`), or uploads it with `-upload URL -key KEY`.
