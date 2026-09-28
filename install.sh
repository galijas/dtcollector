#!/bin/bash
# DT Collector install script for a fresh Ubuntu server (24.04 tested).
# Run from the cloned repo:  sudo ./install.sh
# Safe to re-run: it rebuilds, keeps the database and configuration, and
# restarts the service (this is also how to upgrade after a git pull).
set -euo pipefail

APP=dtcollector
BIN=/usr/local/bin/dtcollector
DATA_DIR=/var/lib/dtcollector
CONF_DIR=/etc/dtcollector
CONF=$CONF_DIR/dtcollector.env
GO_DIR=/usr/local/go

log()  { echo "[install] $*"; }
warn() { echo "[install] WARNING: $*" >&2; }
die()  { echo "[install] ERROR: $*" >&2; exit 1; }

ask() { # ask VAR "prompt" default
  local __var=$1 __prompt=$2 __def=${3:-} __ans
  if [ -n "$__def" ]; then read -r -p "$__prompt [$__def]: " __ans; else read -r -p "$__prompt: " __ans; fi
  printf -v "$__var" '%s' "${__ans:-$__def}"
}

confirm() { # confirm "question" default(y/n)
  local __ans __def=${2:-y}
  read -r -p "$1 [$( [ "$__def" = y ] && echo Y/n || echo y/N )]: " __ans
  __ans=${__ans:-$__def}
  [[ "$__ans" =~ ^[Yy] ]]
}

[ "$(id -u)" -eq 0 ] || die "run as root: sudo ./install.sh"
cd "$(dirname "$0")"
[ -f go.mod ] && [ -d cmd/dtcollector ] || die "run this from the dtcollector repository"

# --- Packages. A minimal Ubuntu image may lack ca-certificates, which
# breaks every HTTPS call including Go's module downloads.
NEED=()
for p in ca-certificates curl git; do dpkg -s "$p" >/dev/null 2>&1 || NEED+=("$p"); done
if [ ${#NEED[@]} -gt 0 ]; then
  log "installing: ${NEED[*]}"
  apt-get update -qq
  apt-get install -y -qq "${NEED[@]}"
fi

# --- Configuration: DNS name and Let's Encrypt email.
DTC_DOMAIN=${DTC_DOMAIN:-}
DTC_EMAIL=${DTC_EMAIL:-}
if [ -f "$CONF" ]; then
  # shellcheck disable=SC1090
  . "$CONF"
fi
echo
echo "DT Collector gets its HTTPS certificate from Let's Encrypt. It needs:"
echo "  - this server's public DNS name (an existing A record pointing at this server), and"
echo "  - an email address for Let's Encrypt (expiry and account notices)."
echo
while :; do
  ask DTC_DOMAIN "Public DNS name" "$DTC_DOMAIN"
  DTC_DOMAIN=$(echo "$DTC_DOMAIN" | tr 'A-Z' 'a-z' | sed 's/\.$//')
  [[ "$DTC_DOMAIN" =~ ^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,}$ ]] && break
  echo "  That doesn't look like a DNS name (example: dt.example.com)."
done
while :; do
  ask DTC_EMAIL "Email address for Let's Encrypt" "$DTC_EMAIL"
  [[ "$DTC_EMAIL" =~ ^[^[:space:]@]+@[^[:space:]@]+\.[^[:space:]@]+$ ]] && break
  echo "  That doesn't look like an email address."
done

# Check the A record against this server's addresses. Behind 1:1 NAT the
# public address isn't configured locally, so this only warns.
RESOLVED=$(getent ahostsv4 "$DTC_DOMAIN" | awk '{print $1}' | sort -u | tr '\n' ' ' || true)
LOCAL=$(hostname -I 2>/dev/null || true)
if [ -z "$RESOLVED" ]; then
  warn "$DTC_DOMAIN does not resolve. Let's Encrypt will fail until the A record exists."
  confirm "Continue anyway?" n || exit 1
else
  MATCH=0
  for ip in $RESOLVED; do [[ " $LOCAL " == *" $ip "* ]] && MATCH=1; done
  if [ $MATCH -eq 0 ]; then
    warn "$DTC_DOMAIN resolves to $RESOLVED, which is not an address on this server ($LOCAL)."
    echo "  That is fine behind NAT, as long as ports 80 and 443 of that address reach this server."
    confirm "Continue?" y || exit 1
  fi
fi

install -d -m 755 "$CONF_DIR"
umask 077
cat > "$CONF" <<EOF
DTC_DOMAIN=$DTC_DOMAIN
DTC_EMAIL=$DTC_EMAIL
EOF
umask 022
chmod 644 "$CONF"
log "configuration saved to $CONF"

# --- Go toolchain.
export PATH="$PATH:$GO_DIR/bin"
if ! command -v go >/dev/null 2>&1; then
  GOVER=$(curl -fsSL "https://go.dev/VERSION?m=text" | head -1)
  ARCH=$(dpkg --print-architecture)
  log "installing Go $GOVER ($ARCH)"
  TMP=$(mktemp)
  curl -fsSL -o "$TMP" "https://go.dev/dl/${GOVER}.linux-${ARCH}.tar.gz"
  rm -rf "$GO_DIR"
  tar -C /usr/local -xzf "$TMP"
  rm -f "$TMP"
fi
log "using $(go version)"

# --- Build.
VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo dev)
log "building dtcollector $VERSION"
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$BIN.new" ./cmd/dtcollector
install -m 755 "$BIN.new" "$BIN"
rm -f "$BIN.new"

# --- Service user and data directory. The app never runs as root.
if ! id "$APP" >/dev/null 2>&1; then
  useradd --system --home-dir "$DATA_DIR" --no-create-home --shell /usr/sbin/nologin "$APP"
  log "created system user $APP"
fi
install -d -m 700 -o "$APP" -g "$APP" "$DATA_DIR"

# --- First admin account (first install only). Printed on exit however the
# script ends, since it is shown only once.
CREDS=""
print_creds() {
  [ -n "$CREDS" ] || return 0
  echo
  echo "$CREDS"
  echo
  log "save this password now; it is not shown again."
  log "(new password later: sudo runuser -u $APP -- $BIN reset-password -data-dir $DATA_DIR -username <name>)"
}
trap print_creds EXIT
if [ ! -f "$DATA_DIR/dtcollector.db" ]; then
  ADMIN_USER=admin
  ask ADMIN_USER "Username for the first admin account" "$ADMIN_USER"
  CREDS=$(runuser -u "$APP" -- "$BIN" create-admin -data-dir "$DATA_DIR" -username "$ADMIN_USER")
fi

# --- systemd units.
HARDENING='NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
PrivateDevices=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectKernelLogs=true
ProtectControlGroups=true
ProtectClock=true
ProtectHostname=true
ProtectProc=invisible
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
RestrictNamespaces=true
RestrictRealtime=true
RestrictSUIDSGID=true
LockPersonality=true
MemoryDenyWriteExecute=true
SystemCallArchitectures=native
SystemCallFilter=@system-service
UMask=0077
ReadWritePaths='"$DATA_DIR"

cat > /etc/systemd/system/$APP.service <<EOF
[Unit]
Description=DT Collector
Wants=network-online.target
After=network-online.target

[Service]
User=$APP
Group=$APP
EnvironmentFile=$CONF
ExecStart=$BIN serve -domain \${DTC_DOMAIN} -email \${DTC_EMAIL} -data-dir $DATA_DIR
Restart=always
RestartSec=2
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
$HARDENING

[Install]
WantedBy=multi-user.target
EOF

cat > /etc/systemd/system/$APP-backup.service <<EOF
[Unit]
Description=DT Collector database backup

[Service]
Type=oneshot
User=$APP
Group=$APP
ExecStart=$BIN backup -data-dir $DATA_DIR -keep 14
CapabilityBoundingSet=
$HARDENING
EOF

cat > /etc/systemd/system/$APP-backup.timer <<EOF
[Unit]
Description=Daily DT Collector database backup

[Timer]
OnCalendar=daily
RandomizedDelaySec=1h
Persistent=true

[Install]
WantedBy=timers.target
EOF

systemctl daemon-reload
systemctl enable --now $APP-backup.timer >/dev/null
systemctl enable $APP >/dev/null 2>&1
systemctl restart $APP
log "service $APP started (systemctl status $APP; journalctl -u $APP)"
log "daily backups to $DATA_DIR/backups (last 14 kept; timer $APP-backup.timer)"

# --- Firewall: only SSH, 80 and 443 in. Containers (a SERVERware VPS is
# one) can't load the netfilter modules ufw needs; there the VPS firewall in
# SERVERware does this job instead.
echo
if systemd-detect-virt --container --quiet 2>/dev/null; then
  warn "this server is a container ($(systemd-detect-virt --container)); ufw can't run here."
  echo "  Set the firewall in SERVERware for this VPS: allow TCP $(sshd -T 2>/dev/null | awk '$1=="port"{print $2}' | sort -u | tr '\n' ' ')80 443 inbound, deny the rest."
elif command -v ufw >/dev/null 2>&1 || apt-get install -y -qq ufw >/dev/null 2>&1; then
  SSH_PORTS=$(sshd -T 2>/dev/null | awk '$1=="port"{print $2}' | sort -u | tr '\n' ' ')
  SSH_PORTS=${SSH_PORTS:-22}
  if confirm "Configure the ufw firewall to allow only SSH (port ${SSH_PORTS% }), 80 and 443 inbound?" y; then
    if { for p in $SSH_PORTS; do ufw allow "$p/tcp"; done
         ufw allow 80/tcp && ufw allow 443/tcp &&
         ufw default deny incoming && ufw default allow outgoing &&
         ufw --force enable; } >/dev/null 2>&1; then
      log "ufw enabled: $(ufw status | grep -c ALLOW) allow rules (ufw status verbose)"
    else
      ufw --force disable >/dev/null 2>&1 || true
      warn "ufw could not be enabled on this system and has been disabled again."
      echo "  Set the firewall elsewhere (hosting or SERVERware firewall): allow TCP ${SSH_PORTS}80 443 inbound, deny the rest."
    fi
  else
    warn "firewall not configured; make sure only SSH, 80 and 443 are reachable"
  fi
fi

# --- First HTTPS request: this is when the certificate is requested.
log "requesting the certificate for https://$DTC_DOMAIN/ (can take up to a minute)"
CODE=000
for _ in $(seq 1 20); do
  CODE=$(curl -s -o /dev/null -w '%{http_code}' --max-time 20 "https://$DTC_DOMAIN/login" || true)
  [ "$CODE" = 200 ] && break
  sleep 3
done
if [ "$CODE" = 200 ]; then
  log "HTTPS works: https://$DTC_DOMAIN/"
else
  warn "https://$DTC_DOMAIN/ is not answering with a valid certificate yet."
  echo "  Check that the A record points here and ports 80 and 443 are reachable from the internet,"
  echo "  then look at: journalctl -u $APP -n 50"
fi

