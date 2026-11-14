# Changelog

## v0.1.3

A security release, after an outside review of the code. Upgrade when you can;
nothing needs to be changed by hand.

- Sign-in providers: an account is no longer found by e-mail address unless
  the provider is set to do so ("Sign people in to the account that has their
  verified e-mail address"). Providers you already have keep doing it,
  new ones start with it off. Administrators are never found by e-mail: they
  link the sign-in under Settings. Before, a provider vouching for an
  administrator's address was enough to become that administrator, without
  password or two-step code
- A sign-in through a provider must end in the browser that started it
- An account that a provider creates keeps its e-mail address only when the
  provider verified it
- When a RustDesk app starts a sign-in through a provider, the page names the
  device and waits for a click. Before, opening a link from someone else could
  sign their app in with your account
- A code of two-step sign-in works once
- The account `admin` of the first start has to get a password of your own
  before it can do anything; the printed one no longer signs in a RustDesk app
- The note on a session can be written by the person who opened the session
  and by administrators, no longer by every signed-in user
- Reports from devices that are not known yet are limited (100 an hour from
  one address, 2000 an hour in total), so the device list cannot be flooded
- No page of the server can be shown in a frame of another site
- README: what the logs are worth as evidence, the limits of unauthenticated
  device reports, and a Caddy setup behind Cloudflare

## v0.1.2

- A published Docker image for x86-64 and ARM64: `ghcr.io/onatakduman/rustdesk-api-lite`,
  and `iamonat/rustdesk-api-lite` on Docker Hub, with a tag for every release and `latest`
  for the newest one. The server itself is
  unchanged from v0.1.1.

## v0.1.1

Easier to install, for people, scripts and AI agents.

- The install script asks nothing, takes the server settings from the environment
  (`RDL_PUBLIC_URL`, `RDL_ID_SERVER`, `RDL_RELAY_SERVER`, `RDL_KEY`, `RDL_LISTEN`), waits until the
  server answers and ends with `status=…`, `url=…`, `admin_password=…` lines; `uninstall` removes
  the service and keeps the data
- Local settings of the service live in `/etc/rustdesk-api-lite.env`
- `GET /healthz` for monitors and scripts
- `rustdesk-api-lite config` shows the settings and the client configuration string;
  `config set` changes them without the console
- A Dockerfile that builds straight from the repository
- `AGENTS.md` with the install procedure for AI agents and the rules for working on the code
- Date format and time zone are a per-browser choice in the console

## v0.1.0

First release.

- Sign-in from the RustDesk app with a password, two-step codes, OpenID Connect providers and GitHub
- Personal and shared address books with rights for people and groups, tags, notes and a default password per book
- Device list with owner, group, notes, live sessions and options pushed to every device
- Logs of connections, file transfers, sign-ins and security alarms
- Web console in English and Turkish: one search over all address books and devices, online and
  offline filters, sorting, tiles or a list with columns, connect in the RustDesk app by double-click
  (remote desktop, file transfer, camera, terminal, TCP tunnel, RDP, through the relay)
- Online state asked from the ID server of every address-book entry, also on other servers
- API tokens, `rustdesk --assign`, user groups, batch actions, CSV export, hbbs/hbbr commands
- Import from lejianwen/rustdesk-api that keeps signed-in clients signed in
- Command line for accounts, tokens and shares; install script and systemd unit
