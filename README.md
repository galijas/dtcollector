# DT Collector

DT Collector is the central server for SERVERware host reports. When a
SwarmDialer instance finishes its standardized load test against a
SERVERware host, it uploads a benchmark report here. SwarmDialer and the
hardware collection script (SWHW Collector) can also upload hardware-only
reports (the host's hardware inventory, no tests). Users browse the
reports, look at each test's results and time series, compare hosts side
by side, and keep a list of validated hardware.

- **Upload API** (`/api/v1`): used by SwarmDialer and the hardware
  collection script, authenticated with API (upload) keys. A key can only
  upload; it can't read, list or delete anything, and it can't log in to
  the web interface. See [docs/api.md](docs/api.md) for the contract.
- **HW Validation** (the first tab): hardware parts (server models, CPUs,
  NICs, drives, storage controllers) marked Supported, Unsupported or Not
  validated, with instant search (dashes and spaces ignored, so "E5 2699"
  finds "E5-2699"), Type / Source / Status filters, and manual add, edit
  and delete.
  - Sources: Supported Hardware Datasheet and SW Analytics (the starter
    list `internal/hardware/seed/hardware-list.json`, imported on first
    install), Manual input (hover shows who entered it), and Test Script
    and SWHW data (parts found in uploaded reports, linked to the report).
  - Every uploaded report adds the parts that aren't listed yet, as
    Supported. Virtual devices (names with virtual, virtio, QEMU, VMware,
    VBox), removable and optical media (USB flash disks, card readers,
    CD/DVD/BD drives) and placeholder models such as "ProductCode" are
    skipped, RAID volumes reported as drives (e.g. "PERC H710P") are listed
    as the storage controller they name, and storage controllers follow the SW Analytics
    rules: only RAID/HBA cards and boot RAID devices are added (no chipset
    SATA/AHCI, BMC virtual media, USB storage or NVMe drives).
  - **Export PDF** downloads the list as shown (search and filters
    applied). **HowTo: Upload SWHW data** opens the steps for running the
    SWHW Collector script.
- **Reports**: 20 per page with a pager, filters for type (benchmark or
  hardware only), source (API key), date range and text search, a
  select-all checkbox, **Compare selected** (2 to 8 benchmark reports) and
  **Delete selected** (Admin only). Times are shown as DD/MM/YYYY in
  Central European time (CEST/CET).
  - Report pages show the host inventory, a results table, and charts per
    test. A clickable (i) next to the stop reason explains how the test
    ended.
  - **Diagnostics reports** (SwarmDialer 1.6.0 and later, marked
    "Diagnostics") also show, per test: a summary of why it ended, the
    load at stop against each limit, failures by cause and SIP code,
    PBXware's own counts, SwarmDialer's health, recordings per instance,
    an event timeline, and PBXware's active calls on the concurrent-calls
    chart. Older reports are shown as before.
  - Compare works within one report kind only: diagnostics reports with
    diagnostics reports, older reports with older reports.
- **Account types**: Admin accounts manage accounts (create, reset
  passwords, change type, delete) and can delete hardware entries and
  reports; User accounts can use everything else and change their own
  password. The server enforces this; the last Admin can't be deleted or
  demoted. Passwords are stored as bcrypt hashes.
- A light/dark theme toggle (remembered per browser) sits in the header.

It is a single Go binary with the web interface embedded, SQLite for
storage, and a Let's Encrypt certificate obtained and renewed by the binary
itself.

## Requirements

- An Ubuntu server (24.04 tested) with a public IP address.
- A DNS A record for the server's name pointing at that address.
- Ports 80 and 443 reachable from the internet (80 is used for Let's
  Encrypt and redirects everything else to HTTPS).

## Install

```bash
sudo apt update && sudo apt install -y git
cd /root
git clone https://github.com/galijas/dtcollector.git
cd dtcollector
sudo ./install.sh
```

The install script:

1. asks for the server's DNS name and an email address for Let's Encrypt
   (saved in `/etc/dtcollector/dtcollector.env`),
2. installs Go if missing and builds `/usr/local/bin/dtcollector`,
3. creates the unprivileged system user `dtcollector` and the data
   directory `/var/lib/dtcollector`,
4. on first install, creates the first Admin account and prints its
   password once (the database starts with the HW Validation starter
   list),
5. installs and starts the `dtcollector` systemd service (running as
   `dtcollector`, with systemd sandboxing; it may bind ports 80 and 443
   through `CAP_NET_BIND_SERVICE` only),
6. enables a daily database backup timer,
7. makes the first HTTPS request, which is when the certificate is issued.

Then open `https://<dns-name>/`, log in, and create an API key under
**API (Upload) Keys** for each site or network that runs SwarmDialer (or
the hardware collection script). Enter that
key in SwarmDialer's Setup Wizard when adding the SERVERware site.

## Upgrade

```bash
cd /root/dtcollector
git pull
sudo ./install.sh
```

The script keeps the configuration (press Enter to accept the saved
values), the database and the certificates, rebuilds, and restarts the
service.

## Operations

| Task | Command |
|---|---|
| Service status | `systemctl status dtcollector` |
| Logs (uploads, logins, errors) | `journalctl -u dtcollector -f` |
| Back up now | `sudo systemctl start dtcollector-backup` |
| List backups | `sudo ls -l /var/lib/dtcollector/backups` |
| Reset an account's password | `sudo runuser -u dtcollector -- dtcollector reset-password -data-dir /var/lib/dtcollector -username <name>` |
| Create an Admin account from the shell | `sudo runuser -u dtcollector -- dtcollector create-admin -data-dir /var/lib/dtcollector -username <name>` |
| Change DNS name or email | `sudo ./install.sh` and enter the new values |
| Export the HW Validation list (new starter list) | `sudo runuser -u dtcollector -- dtcollector export-hardware -data-dir /var/lib/dtcollector -out /tmp/hardware-list.json` |

Backups are consistent SQLite copies (`VACUUM INTO`), taken daily, with the
last 14 kept in `/var/lib/dtcollector/backups`. They are on the same disk
as the database, so copy them off the server regularly, for example:

```bash
rsync -a root@dt.example.com:/var/lib/dtcollector/backups/ ./dtcollector-backups/
```

To restore a backup:

```bash
sudo systemctl stop dtcollector
sudo cp /var/lib/dtcollector/backups/dtcollector-YYYYMMDD-HHMMSS.db /var/lib/dtcollector/dtcollector.db
sudo rm -f /var/lib/dtcollector/dtcollector.db-wal /var/lib/dtcollector/dtcollector.db-shm
sudo chown dtcollector:dtcollector /var/lib/dtcollector/dtcollector.db
sudo systemctl start dtcollector
```

## Files

| Path | Contents |
|---|---|
| `/usr/local/bin/dtcollector` | The binary |
| `/etc/dtcollector/dtcollector.env` | DNS name and Let's Encrypt email |
| `/var/lib/dtcollector/dtcollector.db` | Database: reports, the HW Validation list, accounts, sessions, hashed API keys |
| `/var/lib/dtcollector/autocert/` | Let's Encrypt account key and certificates |
| `/var/lib/dtcollector/backups/` | Daily database backups |
| `/etc/systemd/system/dtcollector*.{service,timer}` | Service and backup units |

## Security summary

- HTTPS only, with a Let's Encrypt certificate renewed automatically;
  HTTP only redirects. HSTS is set.
- Account passwords are bcrypt hashes (cost 12). Failed logins are limited
  to 10 per 15 minutes per client address. Sessions last 12 hours, use
  `__Host-` cookies (`Secure`, `HttpOnly`, `SameSite=Strict`) and end when
  the password changes. Cross-origin form posts are rejected.
- API (upload) keys are 43 random characters, stored as SHA-256 hashes, shown
  once, revocable individually, with "last used" shown.
- The service runs as its own unprivileged user under a sandboxed systemd
  unit. The installer doesn't configure a firewall: restrict inbound
  traffic to SSH, 80 and 443 in the SERVERware (or hosting) firewall.
- Reports contain only hardware details, versions and results (see the
  privacy rule in [docs/api.md](docs/api.md)).

## Development

Go 1.27 or later. Everything runs locally without TLS:

```bash
go test ./...
go build -o bin/dtcollector ./cmd/dtcollector
./bin/dtcollector create-admin -data-dir ./data -username admin
./bin/dtcollector serve -dev-addr 127.0.0.1:8080 -data-dir ./data
```

Log in at `http://127.0.0.1:8080/`, create an API key, then load a few
synthetic reports:

```bash
go run ./cmd/dtc-sample -host 0 -upload http://127.0.0.1:8080 -key dtk_...
go run ./cmd/dtc-sample -host 1 -upload http://127.0.0.1:8080 -key dtk_...
go run ./cmd/dtc-sample -host 2 > example-report.json   # print instead
go run ./cmd/dtc-sample -hardware -upload http://127.0.0.1:8080 -key dtk_...   # SwarmDialer hardware-only report
go run ./cmd/dtc-sample -script -upload http://127.0.0.1:8080 -key dtk_...     # hardware collection script report
go run ./cmd/dtc-sample -diag -upload http://127.0.0.1:8080 -key dtk_...       # SwarmDialer 1.6 report with diagnostics
```

Layout:

| Path | Contents |
|---|---|
| `cmd/dtcollector` | Server and admin CLI (`serve`, `create-admin`, `reset-password`, `backup`, `export-hardware`) |
| `cmd/dtc-sample` | Synthetic report generator and uploader |
| `internal/report` | Report format v1 (including the 1.6 diagnostics fields), validation, summaries, sample generators |
| `internal/hardware` | HW Validation: categories, name matching, extraction from reports and its filters, the starter list (`seed/`) |
| `internal/store` | SQLite schema and queries |
| `internal/auth` | Password hashing, API keys, session tokens |
| `internal/server` | Upload API, sessions and account types, web pages, PDF export, report explanations (`stopinfo.go`, `diag.go`); `web/` holds templates and static files (uPlot for charts) |
