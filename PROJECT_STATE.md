# DT Collector: project state

Read this first when continuing work. The project brief is
`~/claude/DTcollector_project.md` (outside the repo; the single copy);
the upload contract is `docs/api.md`.

## Status (2026-09-30)

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
- Starter list (2026-09-30): migration 3 seeds a new database from
  `internal/hardware/seed/hardware-list.json`, a snapshot of the live list
  (datasheet + SW Analytics as edited by the admins, 370 parts). Migrations
  5-7 are now empty (their one-time updates are in the snapshot). To refresh
  the starter list: on the VPS run `dtcollector export-hardware -data-dir
  /var/lib/dtcollector -out FILE` (as the dtcollector user), copy FILE over
  `internal/hardware/seed/hardware-list.json`, commit. The build scripts in
  `~/claude/Resources/Hardware/` and the JSON files they produce are the
  history of how the list began; they no longer feed DT Collector directly.
- Columns follow what reports can fill: NIC speed, ports seen, Linux driver;
  drive type. Datasheet-only details (chipset, media, interface, capacities,
  controller type/chipset/driver) are in the comment.
- 2026-09-30: parts from reports are added as Supported (was "Not
  validated"); "Not validated" stays available for manual use.
- On every stored upload, `hardware.FromReport` extracts parts; parts whose
  normalized name or alias is already listed are skipped (NIC port counts
  and empty speed/driver/type are merged in). Hardware-only reports are
  "SWHW data", benchmark reports "Test Script". Existing reports were not
  imported, at the user's request.
- Virtual devices are skipped (2026-10-01): any part whose name or raw
  name contains virtual / virtio / qemu / vmware / vbox (`hardware.IsVirtual`).
  Migration 9 removed the ones reports had added (report sources only).
- Storage controllers from reports follow the SW Analytics rules
  (2026-10-01, `hardware.SkipController`): only RAID/HBA cards and boot RAID
  devices are added; chipset SATA/AHCI/IDE/RAID-mode, VMD, BMC virtual
  media, USB storage, virtual controllers, iSCSI functions, NVMe-over-TCP
  and NVMe drives listed as controllers are skipped. Checked against every
  SW Analytics controller string (`internal/hardware/testdata`). Migration
  10 removed the ones reports had added.
- Removable and optical media are skipped (2026-10-07,
  `hardware.Removable`), as drives and as storage controllers: USB flash
  disks, card readers, SD modules, CD/DVD/BD drives (e.g. "Flash Disk",
  "HL-DT-ST DVD+ -RW GU60N", "Chipsbank ... Flash Disk"), plus the
  placeholder model "ProductCode" that cheap USB card readers report (also
  added to the DMI placeholder list). Size can't be used: hw-collect reports
  size 0 for many real SSDs. Migration 11 removed the five such parts
  reports had added; no false positives among the 387 live parts.
- RAID volumes reported as drives (2026-10-07, `hardware.RAIDVolume`):
  RAID cards present their arrays as drives named after the card ("PERC
  H710P") or generically ("LOGICAL VOLUME"). They are not added as drives;
  a card name is added as a storage controller with the vendor prefix the
  list uses ("Dell PERC H710P", "Broadcom (LSI) MR9361-8i", "Fujitsu PRAID
  EP420i"), unless already listed. Migration 12 moved the live "PERC H710P"
  drive (PowerEdge R620 upload) to the controllers as "Dell PERC H710P".
- Matching is conservative (exact after normalization), so a lspci-style
  name like "Intel Ethernet Controller X550" is added next to the
  datasheet's "Intel X550"; admins merge by deleting one. Smarter matching
  (by chipset token) is a possible next step.
- SW Analytics (2026-09-30): migration 4 seeds
  `internal/hardware/seed/sw-analytics-hardware.json` (copy of
  `~/claude/Resources/SW Analytics Hardware.json`, built by
  `~/claude/Resources/Hardware/build_sw_analytics.py` from the Grafana
  "Series joined by time" CSV exports). Source "SW Analytics", status
  Supported. Virtual/USB/broken devices, chipset SATA/AHCI/RAID-mode, VMD,
  iSCSI functions and NVMe drives are excluded (listed under "excluded" in
  the JSON); desktop parts are kept with a note. Parts the datasheet already
  lists stay datasheet rows and get "Also in SW Analytics count: N" in the
  comment plus the SW names as aliases.
  Migration 5 (same day): NVMe drives that SW Analytics lists as storage
  controllers were added to Drives; desktop/laptop CPUs and consumer or
  workstation boards were removed (the JSON's "removed" list; only rows
  still sourced from SW Analytics are deleted).
- Export PDF button (2026-09-30): `GET /hardware/export.pdf?q=&type=&source=&status=`
  renders the list as shown (same search rules as the page, ported to Go
  in `internal/server/hwpdf.go`), A4 landscape, via github.com/go-pdf/fpdf.
- `canEditHardware` in `internal/server/hardware.go` allows every admin;
  account types will narrow it.

## Account types (2026-09-30)

- Migration 8 adds `admins.role` (`admin` | `user`); existing accounts
  became Admin. Admin-only routes use `s.adminOnly` (account create,
  reset, role change, delete; report delete; hardware delete); everything
  else uses `s.admin` (any logged-in account). `canEditHardware` allows
  every account, `canDeleteHardware` Admins only.
- The last Admin can't be deleted or changed to User; nobody can change
  their own type. `dtcollector create-admin` creates Admin accounts.

## Diagnostics reports (2026-10-02)

- SwarmDialer 1.6.0 adds per-test diagnostics within format v1 (spec:
  `~/claude/SwarmDialer_report_diagnostics.md`). `report.Report.HasDiagnostics`
  (version >= 1.6.0 or any field present) decides; `store.ReportRow.HasDiagnostics`
  does the same for the list from the version column (no migration).
- Old reports render exactly as before (the derived `stopInfo` explanation
  for target_not_reached). Diagnostics reports: "Diagnostics" badge (list,
  report header), and per test a summary (`diagInfo`, the spec's order:
  tool, stop + headroom, failures, PBXware's view, quality, tool warnings),
  At stop / Failures / PBXware's view / SwarmDialer health / Recordings
  cards, a collapsible timeline, and PBXware's active calls on the
  concurrent-calls chart. Compare adds "Stop detail" and "Main failure
  cause" rows (shown only when some report has them). Compare refuses to
  mix diagnostics and older reports (server check + list hint).
- `dtc-sample -diag` generates a 1.6-style report (synthetic numbers).
- Attribution (2026-10-02, revised for SwarmDialer 1.6.2,
  `~/claude/DTCollector_attribution_fix.md`): `attribute()` in diag.go.
  "Limited by SwarmDialer" only for swarmdialer_overloaded or SwarmDialer
  CPU >= 80%. Send drops (>= 0.1%) never blame SwarmDialer: the text says
  the host didn't move its packets out (with host CPU at the first drop from
  the 1.6.1 `udp_send_drops` event, or "not recorded" for 1.6.0) and MOS /
  RTP received / loss are marked unreliable (struck through, *, left out of
  compare's best). Drop % estimated for 1.6.0 from calls x 2 legs x 50 pps
  (matches 29.1 / 4.4 / 2.3% on f076747c).
- Failures before 1.6.2 (`failureFixVersion`): rejected 401 / 487 are
  shown with "probably not answered within 15 s (classification
  unreliable)"; `first_at_calls` 0 means the cause only appeared after the
  stop. 1.6.2 `after_stop` counts are shown and causes entirely after the
  stop are left out of "Failures began at". app.js drops the false
  failed_calls spike right after the stop for 1.6.0 / 1.6.1.
- Older reports' rolling tests are re-judged by the 1.6.0 rule (target =
  rate x call length = 510, reached at >= 505): `effectiveStop` /
  `listStop` in stopinfo.go; the (i) explains the reclassification.

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
  (Enter keeps the saved DNS name and email). Take a backup first when the
  release adds a migration: `runuser -u dtcollector -- dtcollector backup
  -data-dir /var/lib/dtcollector`.
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

State at end of 2026-09-30: `main` = the commit after 99f3263 (docs only),
live on the VPS as 99f3263, schema version 8. Backup taken before the last
migration: `backups/dtcollector-20260930-141309.db`.

Built on 2026-09-30 (all live):
- HW Validation tab (first tab): hardware list with search, type/source/
  status filters, manual add/edit/delete, report-derived parts (added as
  Supported), sources Datasheet / SW Analytics / Manual / Test Script /
  SWHW data; starter list = `internal/hardware/seed/hardware-list.json`
  (snapshot of the live list, 370 parts; refresh with `export-hardware`).
- Export PDF button (list as filtered), HowTo: Upload SWHW data window.
- Account types Admin / User.
- SWHW Collector script is built (`~/claude/Projects/SWHW_Collector`, GitHub
  galijas/swhw); DT Collector accepts its reports.

Possible next steps (the user decides):
1. Similar-name hints and a Merge action for hardware entries (discussed;
   the user chose to keep strict matching for now).
2. Optional: match hardware-only entries with benchmark reports from the
   same machine.
3. For the user on the VPS: remove the inert leftover ufw tables
   (`nft delete table ip filter`, `nft delete table ip6 filter`,
   `ufw --force reset`); change the root password (shared in chat) or
   switch to SSH keys.

## Development notes

- Local run without TLS: `dtcollector serve -dev-addr 127.0.0.1:8080 -data-dir ./data`.
- `go run ./cmd/dtc-sample` prints a complete synthetic report (three
  synthetic hosts via `-host 0..2`), or uploads it with `-upload URL -key KEY`.
