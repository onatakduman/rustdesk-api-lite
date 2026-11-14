# Notes for AI agents

Two kinds of work bring an agent here: installing the server for someone, or
changing the code. Both are covered below. Nothing in this project asks
questions on a terminal; every step can run unattended.

## Installing the server

Pick by what the machine is:

| Machine | Do this |
|---|---|
| Linux with systemd, root available | the install script |
| Anything with Docker | run the published image |
| Anything else | download the release archive and run `rustdesk-api-lite serve` |

### Install script

```sh
curl -fsSL https://raw.githubusercontent.com/onatakduman/rustdesk-api-lite/main/install.sh | sudo \
  RDL_PUBLIC_URL=https://rustdesk.example.com \
  RDL_ID_SERVER=rustdesk.example.com \
  RDL_RELAY_SERVER=rustdesk.example.com \
  RDL_KEY="$(cat /var/lib/rustdesk/id_ed25519.pub)" \
  sh
```

All four variables are optional and can be set later (`config set`, below).
`RDL_KEY` is the public key of the user's hbbs; the path differs between
installations, so look for `id_ed25519.pub` next to the hbbs database.

The script exits non-zero on failure and ends with lines to parse:

```
status=installed            # or upgraded, uninstalled, failed
version=v0.1.2
url=http://127.0.0.1:21114
health=http://127.0.0.1:21114/healthz
service=rustdesk-api-lite
settings=/etc/rustdesk-api-lite.env
admin_user=admin            # first install only
admin_password=…            # first install only, shown once: hand it to the user
```

Running it again upgrades. `… | sudo sh -s -- uninstall` removes the service
and keeps the data.

### Docker

```sh
docker run -d --name rustdesk-api-lite --restart unless-stopped \
  -p 127.0.0.1:21114:21114 -v rustdesk-api-lite:/data \
  -e RDL_PUBLIC_URL=https://rustdesk.example.com -e RDL_ID_SERVER=rustdesk.example.com \
  -e RDL_RELAY_SERVER=rustdesk.example.com -e RDL_KEY=… \
  ghcr.io/onatakduman/rustdesk-api-lite
docker logs rustdesk-api-lite 2>&1 | grep 'First start'     # the admin password
```

### Check and finish

1. `curl -fsS http://127.0.0.1:21114/healthz` answers `{"status":"ok","version":"…"}`.
2. The server speaks plain HTTP on localhost. It needs a reverse proxy with
   HTTPS in front before RustDesk apps use it; the README has a Caddy example.
   Do not publish port 21114 directly.
3. Settings from the command line (as the service user, against its database):

   ```sh
   run="sudo -u rustdesk-api-lite rustdesk-api-lite"
   db="-db /var/lib/rustdesk-api-lite/rustdesk-api-lite.db"
   $run config $db                                    # shows the settings and client-config=…
   $run config set public-url https://rustdesk.example.com $db
   $run user add alice $db < password-file            # also: user passwd, user list
   $run token add alice "nightly sync" $db            # prints an API token once
   $run share "Field" alice edit $db                  # view | edit | full | none
   ```

   With Docker: `docker exec rustdesk-api-lite /rustdesk-api-lite config`.
4. `client-config=…` is the string that sets up a RustDesk app for this
   server: paste it under Settings → Network → ID/Relay server → Import, or run
   `rustdesk --config <string>` on the device.

Things to leave to the user: the admin password (pass it on, do not store
it), DNS and the certificate of the public address, and the choice of who
gets an account.

## Working on the code

- `make test` runs `go vet` and the tests; `make linux` and `make release`
  build static binaries. Go only, no C toolchain.
- The console is plain HTML, CSS and JavaScript in `web/`, embedded in the
  binary. There is no build step and no dependency to install. English text
  in `web/app.js` is the translation key; add the Turkish text to the `TR` map.
- `docs/client-api-contract.md` is what the RustDesk app expects on the wire.
  Responses to the app are strict: address-book changes and audit reports
  answer 200 with an empty body, and nothing may answer 5xx.
- Code, comments and commit messages are in English.
- A release is a pushed tag `vX.Y.Z` plus a `## vX.Y.Z` section in
  `CHANGELOG.md`; the workflow builds and publishes the archives and the
  Docker image (`ghcr.io/onatakduman/rustdesk-api-lite`, x86-64 and ARM64).
