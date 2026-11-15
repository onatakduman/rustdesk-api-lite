#!/bin/sh
# Installs, upgrades or removes rustdesk-api-lite on a Linux machine with systemd.
# It asks no questions and ends with NAME=value lines a script or an agent can read.
#
#   curl -fsSL https://raw.githubusercontent.com/onatakduman/rustdesk-api-lite/main/install.sh | sudo sh
#
# Run it again to upgrade: the binary is replaced and the service restarted; the
# database, the settings file and an existing service file are left alone.
#
#   ... | sudo sh -s -- uninstall      stops and removes the service; the data stays
#
# Environment:
#   RDL_VERSION       release to install, for example v0.2.0 (default: the latest)
#   RDL_BINARY        install this local binary instead of downloading a release;
#                     run from a source checkout, or point RDL_UNIT at the service file
#   RDL_LISTEN        address to listen on (default 127.0.0.1:21114)
#   RDL_PUBLIC_URL    the address people and RustDesk apps use, e.g. https://rustdesk.example.com
#   RDL_ID_SERVER     your hbbs, e.g. rustdesk.example.com
#   RDL_RELAY_SERVER  your hbbr
#   RDL_KEY           the public key of hbbs (the content of id_ed25519.pub)
# The last five are written to /etc/rustdesk-api-lite.env on the first install.
set -eu

REPO=onatakduman/rustdesk-api-lite
BIN=/usr/local/bin/rustdesk-api-lite
UNIT=/etc/systemd/system/rustdesk-api-lite.service
ENV_FILE=/etc/rustdesk-api-lite.env
DATA_DIR=/var/lib/rustdesk-api-lite
SERVICE=rustdesk-api-lite

say() { printf '%s\n' "$*"; }
die() {
    printf 'install: %s\n' "$*" >&2
    say "status=failed"
    exit 1
}

[ "$(id -u)" = 0 ] || die "run as root (sudo)"
[ "$(uname -s)" = Linux ] || die "this script is for Linux; the README covers other systems"
command -v systemctl >/dev/null 2>&1 || die "systemd is needed; the README shows Docker and a start by hand"

if [ "${1:-}" = uninstall ]; then
    systemctl disable --now "$SERVICE" >/dev/null 2>&1 || true
    rm -f "$BIN" "$UNIT"
    systemctl daemon-reload
    say "status=uninstalled"
    say "kept_data=$DATA_DIR"
    say "kept_settings=$ENV_FILE"
    exit 0
fi

case "$(uname -m)" in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) die "there is no release for $(uname -m); build from source" ;;
esac

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fetch() {
    if command -v curl >/dev/null 2>&1; then curl -fsSL -o "$2" "$1"; else wget -qO "$2" "$1"; fi
}

if [ -n "${RDL_BINARY:-}" ]; then
    [ -f "$RDL_BINARY" ] || die "$RDL_BINARY not found"
    cp "$RDL_BINARY" "$tmp/$SERVICE"
    unit_src=${RDL_UNIT:-deploy/rustdesk-api-lite.service}
else
    name=rustdesk-api-lite-linux-$arch
    if [ -n "${RDL_VERSION:-}" ]; then
        base=https://github.com/$REPO/releases/download/$RDL_VERSION
    else
        base=https://github.com/$REPO/releases/latest/download
    fi
    say "Downloading $name.tar.gz"
    fetch "$base/$name.tar.gz" "$tmp/$name.tar.gz" || die "the download failed"
    fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" || die "the download of the checksums failed"
    (cd "$tmp" && grep " $name.tar.gz\$" SHA256SUMS | sha256sum -c - >/dev/null) || die "the checksum does not match"
    tar -C "$tmp" -xzf "$tmp/$name.tar.gz"
    cp "$tmp/$name/$SERVICE" "$tmp/$SERVICE"
    unit_src=$tmp/$name/rustdesk-api-lite.service
fi

status=upgraded
[ -f "$BIN" ] || status=installed
install -m 755 "$tmp/$SERVICE" "$BIN"
id "$SERVICE" >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin "$SERVICE"
if [ -f "$UNIT" ]; then
    say "Keeping the existing $UNIT"
else
    [ -f "$unit_src" ] || die "service file $unit_src not found"
    install -m 644 "$unit_src" "$UNIT"
fi
if [ ! -f "$ENV_FILE" ]; then
    {
        say "# Settings of rustdesk-api-lite, read by its service. NAME=value, one per line."
        for var in RDL_LISTEN RDL_PUBLIC_URL RDL_ID_SERVER RDL_RELAY_SERVER RDL_KEY; do
            eval "value=\${$var:-}"
            [ -z "$value" ] || say "$var=$value"
        done
    } >"$ENV_FILE"
    chmod 644 "$ENV_FILE"
fi

systemctl daemon-reload
systemctl enable "$SERVICE" >/dev/null 2>&1
systemctl restart "$SERVICE"

# Where it listens: the settings file wins over the service file, as it does for the service.
listen=$({
    cat "$ENV_FILE" 2>/dev/null
    sed -n 's/^Environment=//p' "$UNIT" 2>/dev/null
} | sed -n 's/^RDL_LISTEN=//p' | head -n 1)
listen=${listen:-127.0.0.1:21114}
case "$listen" in
    :* | 0.0.0.0:* | \[::\]:*) url=http://127.0.0.1:${listen##*:} ;;
    *) url=http://$listen ;;
esac

ok=0
for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
    if fetch "$url/healthz" "$tmp/health" 2>/dev/null; then
        ok=1
        break
    fi
    sleep 1
done
[ "$ok" = 1 ] || die "the service does not answer on $url; look at: journalctl -u $SERVICE"

# The admin password is printed once, by the very first start. Only a line from this run counts.
password=$(journalctl -u "$SERVICE" --since "2 minutes ago" --no-pager -o cat 2>/dev/null | sed -n 's/.*with password  *//p' | tail -n 1)

say
say "rustdesk-api-lite is running. Put a reverse proxy with HTTPS in front of it (the README"
say "has an example), sign in, and check ID server, relay server and key under Settings."
say
say "status=$status"
say "version=$("$BIN" version)"
say "url=$url"
say "health=$url/healthz"
say "service=$SERVICE"
say "settings=$ENV_FILE"
if [ "$status" = installed ] && [ -n "$password" ]; then
    say "admin_user=admin"
    say "admin_password=$password"
fi
