# Changelog

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
