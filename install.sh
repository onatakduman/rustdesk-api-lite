#!/bin/sh
# Installs or upgrades rustdesk-api-lite on a Linux machine with systemd.
#
#   curl -fsSL https://raw.githubusercontent.com/onatakduman/rustdesk-api-lite/main/install.sh | sudo sh
#
# Run it again to upgrade: the binary is replaced and the service restarted;
# the database and an existing service file are left alone.
#
# Environment:
#   RDL_VERSION   release to install, for example v0.1.0 (default: the latest)
#   RDL_BINARY    install this local binary instead of downloading a release;
#                 run from a source checkout, or point RDL_UNIT at the service file
set -eu

REPO=onatakduman/rustdesk-api-lite
BIN=/usr/local/bin/rustdesk-api-lite
UNIT=/etc/systemd/system/rustdesk-api-lite.service
SERVICE_USER=rustdesk-api-lite

say() { printf '%s\n' "$*"; }
die() { printf 'install: %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" = 0 ] || die "run as root (sudo)"
[ "$(uname -s)" = Linux ] || die "this script is for Linux; the README covers other systems"
command -v systemctl >/dev/null 2>&1 || die "systemd is needed; the README shows how to start the server by hand"

case "$(uname -m)" in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) die "there is no release for $(uname -m); build from source" ;;
esac

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

if [ -n "${RDL_BINARY:-}" ]; then
    [ -f "$RDL_BINARY" ] || die "$RDL_BINARY not found"
    cp "$RDL_BINARY" "$tmp/rustdesk-api-lite"
    unit_src=${RDL_UNIT:-deploy/rustdesk-api-lite.service}
else
    name=rustdesk-api-lite-linux-$arch
    if [ -n "${RDL_VERSION:-}" ]; then
        base=https://github.com/$REPO/releases/download/$RDL_VERSION
    else
        base=https://github.com/$REPO/releases/latest/download
    fi
    fetch() {
        if command -v curl >/dev/null 2>&1; then curl -fsSL -o "$2" "$1"; else wget -qO "$2" "$1"; fi
    }
    say "Downloading $name.tar.gz"
    fetch "$base/$name.tar.gz" "$tmp/$name.tar.gz" || die "the download failed"
    fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" || die "the download of the checksums failed"
    (cd "$tmp" && grep " $name.tar.gz\$" SHA256SUMS | sha256sum -c - >/dev/null) || die "the checksum does not match"
    tar -C "$tmp" -xzf "$tmp/$name.tar.gz"
    cp "$tmp/$name/rustdesk-api-lite" "$tmp/rustdesk-api-lite"
    unit_src=$tmp/$name/rustdesk-api-lite.service
fi

fresh=1
[ -f "$BIN" ] && fresh=0
install -m 755 "$tmp/rustdesk-api-lite" "$BIN"
id "$SERVICE_USER" >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin "$SERVICE_USER"
if [ -f "$UNIT" ]; then
    say "Keeping the existing $UNIT"
else
    [ -f "$unit_src" ] || die "service file $unit_src not found"
    install -m 644 "$unit_src" "$UNIT"
fi
systemctl daemon-reload
systemctl enable rustdesk-api-lite >/dev/null 2>&1
systemctl restart rustdesk-api-lite
sleep 2
systemctl is-active --quiet rustdesk-api-lite || die "the service did not start; look at: journalctl -u rustdesk-api-lite"

say "rustdesk-api-lite $("$BIN" version) is running."
if [ "$fresh" = 1 ]; then
    say
    say "The first start created the account \"admin\". Its password is in the service log:"
    journalctl -u rustdesk-api-lite --no-pager -o cat 2>/dev/null | grep -m1 'First start' ||
        say "  journalctl -u rustdesk-api-lite | grep 'First start'"
    say
    say "Next: put a reverse proxy with HTTPS in front of 127.0.0.1:21114 (the README has"
    say "an example), sign in, and enter your ID server, relay server and key under Settings."
fi
