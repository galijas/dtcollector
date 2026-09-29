# DT Collector

DT Collector is the central server for SwarmDialer hardware test reports.
When a SwarmDialer instance finishes its standardized load test against a
SERVERware host, it uploads one report here. Admins can then browse the
reports, look at each test's results and time series, and compare hosts
side by side.

- **Upload API** (`/api/v1`): used only by SwarmDialer, authenticated with
  upload keys. A key can only upload; it can't read, list or delete
  anything, and it can't log in to the web interface. See
  [docs/api.md](docs/api.md) for the contract.
- **Web interface**: local admin accounts (bcrypt-hashed passwords). Admins
  browse and compare reports and manage upload keys and admin accounts.

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
4. on first install, creates the first admin account and prints its
   password once,
5. installs and starts the `dtcollector` systemd service (running as
   `dtcollector`, with systemd sandboxing; it may bind ports 80 and 443
   through `CAP_NET_BIND_SERVICE` only),
6. enables a daily database backup timer,
7. makes the first HTTPS request, which is when the certificate is issued.

Then open `https://<dns-name>/`, log in, and create an upload key under
**Upload keys** for each site or network that runs SwarmDialer. Enter that
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
| Reset an admin's password | `sudo runuser -u dtcollector -- dtcollector reset-password -data-dir /var/lib/dtcollector -username <name>` |
| Create an admin from the shell | `sudo runuser -u dtcollector -- dtcollector create-admin -data-dir /var/lib/dtcollector -username <name>` |
| Change DNS name or email | `sudo ./install.sh` and enter the new values |

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
| `/var/lib/dtcollector/dtcollector.db` | Database: reports, admins, sessions, hashed upload keys |
| `/var/lib/dtcollector/autocert/` | Let's Encrypt account key and certificates |
| `/var/lib/dtcollector/backups/` | Daily database backups |
| `/etc/systemd/system/dtcollector*.{service,timer}` | Service and backup units |

## Security summary

- HTTPS only, with a Let's Encrypt certificate renewed automatically;
  HTTP only redirects. HSTS is set.
- Admin passwords are bcrypt hashes (cost 12). Failed logins are limited
  to 10 per 15 minutes per client address. Sessions last 12 hours, use
  `__Host-` cookies (`Secure`, `HttpOnly`, `SameSite=Strict`) and end when
  the password changes. Cross-origin form posts are rejected.
- Upload keys are 43 random characters, stored as SHA-256 hashes, shown
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

Log in at `http://127.0.0.1:8080/`, create an upload key, then load a few
synthetic reports:

```bash
go run ./cmd/dtc-sample -host 0 -upload http://127.0.0.1:8080 -key dtk_...
go run ./cmd/dtc-sample -host 1 -upload http://127.0.0.1:8080 -key dtk_...
go run ./cmd/dtc-sample -host 2 > example-report.json   # print instead
```

Layout:

| Path | Contents |
|---|---|
| `cmd/dtcollector` | Server and admin CLI (`serve`, `create-admin`, `reset-password`, `backup`) |
| `cmd/dtc-sample` | Synthetic report generator and uploader |
| `internal/report` | Report format v1, validation, summaries, sample generator |
| `internal/store` | SQLite schema and queries |
| `internal/auth` | Password hashing, upload keys, session tokens |
| `internal/server` | Upload API, admin sessions, web pages; `web/` holds templates and static files (uPlot for charts) |
