# DT Collector: project state

Read this first when continuing work. The project brief is
`~/claude/DTcollector_project.md` (outside the repo; the single copy);
the upload contract is `docs/api.md`.

## Status (2026-09-28)

Deployed at https://dtcollector.dtbicom.xyz/ (SERVERware VPS, KVM,
SSH on port 2020). HTTPS with the Let's Encrypt certificate works.
2026-09-29: UI restyled to match SwarmDialer (dark theme, centered header
with the white DT Collector icon, tab navigation); deployed as bbfacd9.
Then: tabs Reports / API (Upload) Keys / Accounts (Admins and Account
merged), light/dark toggle like SwarmDialer's (black icons in light mode),
hardware-only reports (profile `hardware`, `tests: []`, optional `source`
instead of `swarmdialer_version`, empty `vps`/`pbxware` allowed),
`target_not_reached` stop reason, new host fields shown, rejected uploads
logged. DB migration 2 adds kind/source/system columns (backfilled).

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

## HW Validation (2026-09-30)

- First tab (`/`); the report list moved to `/reports`.
- Table `hw_parts` (migration 3): category (server_model, cpu, nic, drive,
  storage_controller), name, status (supported, unsupported, unverified =
  "Not validated"), comment, attrs (NIC speed/ports/driver, drive type),
  aliases (texts it was seen as; used for matching and search), source
  (datasheet, manual, test_script, swhw), source report, created/updated by.
- Migration 3 seeds the datasheet from `internal/hardware/seed/*.json`
  (copies of `~/claude/Resources/{Supported,Unsupported} Hardware.json`).
  A part in both lists is kept once as unsupported (the HPE 331FLR/331i).
  The seed runs once per database; changing the JSON later only affects new
  installs.
- Columns follow what reports can fill: NIC speed, ports seen, Linux driver;
  drive type. Datasheet-only details (chipset, media, interface, capacities,
  controller type/chipset/driver) are in the comment.
- On every stored upload, `hardware.FromReport` extracts parts; parts whose
  normalized name or alias is already listed are skipped (NIC port counts
  and empty speed/driver/type are merged in). Hardware-only reports are
  "SWHW data", benchmark reports "Test Script". Existing reports were not
  imported, at the user's request.
- Matching is conservative (exact after normalization), so a lspci-style
  name like "Intel Ethernet Controller X550" is added next to the
  datasheet's "Intel X550"; admins merge by deleting one. Smarter matching
  (by chipset token) is a possible next step.
- `canEditHardware` in `internal/server/hardware.go` allows every admin;
  account types will narrow it.

## Open items

- Units of `host_net_*_bps` (assumed bits/s) and `host_disk_*_bps`
  (assumed bytes/s): confirm when building SwarmDialer's side.
- The "(tbc)" host fields (CPU model, disks, disk type): confirm what
  SwarmDialer can read from SERVERware's Prometheus; the server accepts
  them missing.
- Matching hardware-only entries with benchmark reports from the same
  machine (optional, not built).
- The hardware collection script (`~/claude/Project_HW_collect.md`) is not
  built yet; DT Collector accepts its format (see docs/api.md).
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
  so ufw can't work. Decision (2026-09-29): the firewall is SERVERware's
  job; install.sh no longer configures one (ufw/nftables code removed).
- Claude may deploy directly over SSH (root, port 2020) when the user
  provides the password in the session; it is never stored in files.

## Next session

State at end of 2026-09-29: `main` = a44906b, deployed and live (backup
taken before its DB migration: `backups/dtcollector-20260929-115405.db`).
SwarmDialer's upload side is built (SwarmDialer v1.4/v1.5).

Candidates, as the user decides:
1. Build the hardware collection script (brief `~/claude/Project_HW_collect.md`);
   DT Collector already accepts its format (`dtc-sample -script` shows it).
2. Optional: match hardware-only entries with benchmark reports from the
   same machine.
3. Left for the user on the VPS (the auto-mode check blocked it): remove
   the inert leftover ufw tables (`nft delete table ip filter`,
   `nft delete table ip6 filter`, `ufw --force reset`). Recommended: change
   the root password, which was shared in chat, or switch to SSH keys.

## Development notes

- Local run without TLS: `dtcollector serve -dev-addr 127.0.0.1:8080 -data-dir ./data`.
- `go run ./cmd/dtc-sample` prints a complete synthetic report (three
  synthetic hosts via `-host 0..2`), or uploads it with `-upload URL -key KEY`.
