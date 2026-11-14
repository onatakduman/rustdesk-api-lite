# RustDesk API Lite

A small account, address-book and console server for self-hosted
[RustDesk](https://github.com/rustdesk/rustdesk). It gives the open-source
`hbbs`/`hbbr` pair the part they do not include: sign-in from the RustDesk app,
address books that sync between your devices, a device list and a connection
log, with a web console that looks and works like the RustDesk app.

One static binary, one SQLite file, about 15 MB of memory. No Docker, no
Node, no external database.

> This is an independent project. It is not affiliated with or endorsed by
> RustDesk. "RustDesk" and the RustDesk logo are trademarks of their owner.
> The console shows the logo (`web/icon.svg`, taken unchanged from the RustDesk
> project) only to say which software it is a companion to; that file is not
> covered by this project's MIT license.

## What it does

- **Sign-in from the RustDesk app** with a password, an authenticator code
  (two-step sign-in) or a provider: any OpenID Connect issuer (Google, …) and GitHub.
- **Address books**: a personal one per user plus shared ones with
  view / edit / full-control rights, tags with colours, aliases, notes and a
  default password per shared book. Works with the current RustDesk clients
  and with the legacy single-blob protocol of old ones.
- **Devices**: every installation that points at this server shows up with
  host name, OS, version, owner, group and online state. An administrator can
  set owner and group, end a running session and push client options to all devices.
- **Logs**: who connected to which device and when (with the note written at
  the end of a session), file transfers, sign-ins, and the security alarms
  devices report (blocked addresses, repeated failed attempts).
- **User groups**: in a shared group the members see each other's devices in
  the RustDesk app; an address book can be shared with a person or a whole group.
- **Batch work**: select several devices or entries to add them to a book,
  tag, copy, remove or forget them; export a book as CSV.
- **Server commands**: send the administration commands of hbbs and hbbr
  (relay list, block lists, bandwidth limits, …) from the console when they run on the same machine.
- **API tokens** for scripts and for `rustdesk --assign --token …`.
- **Enrolment**: custom clients can place themselves under an owner, a group
  and a shared address book through their `preset-*` options (off by default).
- **Console** for all of the above, usable on a phone, in English and Turkish.
- **Find and connect from the console**: one search over every address book you can
  read and the devices (`/` or Ctrl/Cmd+K), filters for online and offline, sorting,
  tiles or a list with columns. A double-click or the arrow opens the device in the
  RustDesk app; the menu also offers file transfer, camera, terminal, TCP tunnel, RDP
  and a connection through the relay. Both side panels fold away.
- **Entries on other servers**: an address-book entry can carry its own server
  and key (`123456789@host?key=…`); the console edits these as separate fields
  and can pin a server on every bare ID of a book in one step.
- **Import** from [lejianwen/rustdesk-api](https://github.com/lejianwen/rustdesk-api):
  users, password hashes, linked identities, sign-in providers, address books,
  shares, devices, the connection log and the live sessions, so signed-in
  clients stay signed in.

Not included: session recording, LDAP, e-mail verification codes, fixed-ID
deployment (`rustdesk --deploy`, needs the rendezvous server's cooperation).
See `docs/client-api-contract.md` for everything the client can call.

## Quick start

```
go build -o rustdesk-api-lite .
./rustdesk-api-lite serve
```

The first start creates the account `admin` and prints its password once.
Open `http://127.0.0.1:21114`, sign in, and fill in your ID server, relay
server and public key under Settings.

In the RustDesk app set the API server (Settings → Network → ID/Relay server)
to the public address of this server, for example `https://rustdesk.example.com`.

### Behind Caddy

```
rustdesk.example.com {
    reverse_proxy 127.0.0.1:21114
}
```

Run with `-trust-proxy` (or `RDL_TRUST_PROXY=1`) so the real client address is
used for throttling and logs. A systemd unit is in `deploy/`.

### Options

| Flag | Environment | Default | |
|---|---|---|---|
| `-db` | `RDL_DB` | `./data/rustdesk-api-lite.db` | database file |
| `-listen` | `RDL_LISTEN` | `127.0.0.1:21114` | listen address |
| `-trust-proxy` | `RDL_TRUST_PROXY` | off | trust `X-Forwarded-*` |

`RDL_PUBLIC_URL`, `RDL_ID_SERVER`, `RDL_RELAY_SERVER` and `RDL_KEY` seed the
settings on the first start; later they are edited in the console.

### Accounts from the command line

```
rustdesk-api-lite user add alice -admin     # password is read from stdin
rustdesk-api-lite user passwd alice
rustdesk-api-lite user list
```

## Moving from lejianwen/rustdesk-api

```
rustdesk-api-lite import-lejianwen /path/to/rustdeskapi.db
```

Run it as the service user and against the service database (`-db …`) when
the server is installed with the systemd unit; take a copy of the source
database first if the old server is still running.

The source database is opened read-only. The import can be repeated: accounts
are matched by user name, imported address books are replaced with the source
content, the connection log continues where the last run stopped. Register the
redirect address shown under Settings → Sign-in providers at your provider; the
path `/api/oidc/callback` is the same as before.

## Security notes

- Passwords are stored as bcrypt hashes, session tokens as SHA-256 hashes.
- Sign-in is throttled per address and per account.
- The console uses an HttpOnly cookie; changes need a same-origin request.
- Device reports (heartbeat, system information, audit) are unauthenticated by
  design of the RustDesk client. A device ID is bound to the first UUID that
  reports it; reports with another UUID are ignored.
- Serve it over HTTPS. The RustDesk app sends the session token on every call.

## Development

```
make test
make linux      # static linux/amd64 and linux/arm64 binaries in dist/
```

The console is plain HTML, CSS and JavaScript in `web/`, embedded into the
binary. There is no build step.

## License

MIT
