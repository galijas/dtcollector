# DT Collector: project state

Read this first when continuing work. The original brief is
`DTcollector_project.md`; the upload contract is `docs/api.md`.

## Status (2026-09-28)

Deployed at https://dtcollector.dtbicom.xyz/ (SERVERware VPS, KVM,
SSH on port 2020). HTTPS with the Let's Encrypt certificate works.
2026-09-29: UI restyled to match SwarmDialer (dark theme, centered header
with the white DT Collector icon, tab navigation); deployed as bbfacd9.

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
- SwarmDialer side not built yet: upload key entry in the Setup Wizard
  (checked with `/api/v1/ping`), report building, upload with retries.
- GitHub: `galijas/dtcollector`, `main` pushed 2026-09-28.

## Live system

- Upgrade: on the server, `cd /root/dtcollector && git pull && sudo ./install.sh`
  (Enter keeps the saved DNS name and email). Claude doesn't run commands
  there; it gives the commands to run.
- 2026-09-28 first install: `ufw enable` failed and `set -e` aborted the
  script before the admin password was printed (password reset with
  `reset-password`; the password is now printed on exit in every case).
  Cause, found 2026-09-29: the VPS is KVM (not a container) with a
  SERVERware kernel (6.1.x) that has no `/lib/modules` and no LOG target,
  so ufw can't work. nftables works, so install.sh now probes ufw and falls
  back to its own nftables ruleset (`dtcollector-firewall.service`).
- Claude may deploy directly over SSH (root, port 2020) when the user
  provides the password in the session; it is never stored in files.

## Next session

1. Admin creates upload keys in the web interface (one per site/network).
2. Build SwarmDialer's side in `~/claude/SwarmDialer` against `docs/api.md`:
   key entry and `/api/v1/ping` check in the Setup Wizard, report built
   from an allowlist of fields, upload with retries, local copy kept.
3. Settle the open items above while doing 2, and update `docs/api.md`
   and the validation in `internal/report` together if anything changes.

## Development notes

- Local run without TLS: `dtcollector serve -dev-addr 127.0.0.1:8080 -data-dir ./data`.
- `go run ./cmd/dtc-sample` prints a complete synthetic report (three
  synthetic hosts via `-host 0..2`), or uploads it with `-upload URL -key KEY`.
