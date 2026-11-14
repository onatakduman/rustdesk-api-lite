# Moving from lejianwen/rustdesk-api

```
rustdesk-api-lite import-lejianwen /path/to/rustdeskapi.db
```

Run it as the service user and against the service database (`-db …`) when
the server is installed with the systemd unit; take a copy of the source
database first if the old server is still running.

What comes over: users with their password hashes, linked identities, sign-in
providers, address books and their shares, groups, devices, the connection
log and the live sessions, so signed-in RustDesk apps stay signed in.

The source database is opened read-only. The import can be repeated: accounts
are matched by user name, imported address books are replaced with the source
content, the connection log continues where the last run stopped. Register the
redirect address shown under Settings → Sign-in providers at your provider; the
path `/api/oidc/callback` is the same as before.
