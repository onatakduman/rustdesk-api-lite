# RustDesk client 1.5.3 → self-hosted API server: wire contract

Ground truth: `rustdesk/rustdesk` tag `1.5.3` = commit `95cca3f661f635b3cc85c04935fe8d96d8f432ef` (2026-11-09).
Submodule `libs/hbb_common` = `rustdesk/hbb_common@fae47e024dd83cef6aaecde0aa7d11731df11649`.
Cross-check: `lejianwen/rustdesk-api` `master` = `c5687e150615bfb00d9d105f0883a2802750a8ad` (last push 2025-09-29).
All `path:line` citations are relative to the rustdesk repo unless prefixed `hbb_common/` or `lejianwen/`.
Everything below was read from source; nothing was run against a server. Items I could not confirm are listed in section 13.

---

## 1. Conventions that apply to every endpoint

### 1.1 Base URL

`get_api_server(api, custom)` — `src/common.rs:1270-1312`:

1. Built-in option `register-device` = `N` → API server is `""` and every call below is skipped (`src/common.rs:1271-1273`, `hbb_common/src/config.rs:1303-1310`).
2. Windows only: API from the licence encoded in the exe name (`src/common.rs:1288-1293`).
3. Option `api-server` if set.
4. Else derived from the custom ID server: `http://<host>:<rendezvous_port - 2>` → `http://host:21114` by default (`src/common.rs:1297-1310`, `hbb_common/src/config.rs:119`).
5. Else `https://admin.rustdesk.com`.

Post-processing: one trailing `/` removed; if the result starts with `https` and ends with `:21114`, the `:21114` is **stripped** unless built-in option `allow-https-21114` = `Y` (`src/common.rs:1275-1283`). So `https://host:21114` is silently called as `https://host`.

The settings UI only accepts values starting with `http://` or `https://` (`flutter/lib/common.dart:3660-3666`). Changing the API server while logged in fires `POST /api/logout` at the old server (`flutter/lib/common.dart:3676-3681`).

All paths are `<base>/api/...`. There is no version prefix.

### 1.2 Two HTTP stacks hit the server

| Caller | Stack | Timeout | Used for |
|---|---|---|---|
| Flutter UI, default | Dart `package:http` | 30 s (`flutter/lib/utils/http_service.dart:52`) | login, currentUser, logout, login-options, all `/api/ab*`, group tab, `/api/audit` note calls |
| Flutter UI when a proxy is set or local option `enable-flutter-http-on-rust` is on | Rust `reqwest` through `main_http_request` | 12 s per attempt (`src/common.rs:1883-1885`) | same endpoints (`flutter/lib/utils/http_service.dart:20-45`) |
| Rust core (service and session processes) | Rust `reqwest` | 12 s per attempt (`src/common.rs:1775`) | heartbeat, sysinfo, audit, OIDC, record, switch-grant, CLI |
| Web client | Dart `package:http` in the browser | 30 s | same as Flutter UI; needs CORS (section 13) |

Rust stack specifics that a server sees:

- `post_request` always sends `Content-Type: application/json` plus at most one extra header parsed from a `"Key: Value"` string (`src/common.rs:1768-1774`). Device calls pass `""` → **no Authorization header**.
- `post_request` **ignores the HTTP status** and returns the body text; only transport errors are errors (`src/common.rs:1604-1622`, `1673`).
- TLS ladder on transport error: rustls → rustls accepting invalid certs → native-tls (`src/common.rs:1800-1838`).
- **TCP-proxy fallback**: on transport error **or any status ≥ 500**, the request is re-sent as a protobuf `HttpProxyRequest` to the rendezvous server (hbbs) over TCP, if WebSocket mode is off and the URL host equals the API host (`src/common.rs:1626-1674`, `1393-1416`, `1466-1523`; proto `hbb_common/protos/rendezvous.proto:257-274`). Overall timeout 36 s (`CONNECT_TIMEOUT + READ_TIMEOUT`, `hbb_common/src/config.rs:44-45`). Built-in option `use-raw-tcp-for-api` = `Y` sends everything that way (`src/common.rs:1385-1389`). This is an hbbs-side Pro feature; an API server only has to **never answer 5xx for normal conditions**, otherwise clients stall on the fallback.
- Before OIDC and before record upload the core sends **`HEAD <base>/api/login-options`** as a TLS probe; any HTTP response counts as success (`src/hbbs_http/account.rs:149-158`, `src/hbbs_http/record_upload.rs:33-35`, `src/hbbs_http/http_client.rs:180`, `319`).

### 1.3 Authentication header

`Authorization: Bearer <access_token>` — built in one place, `getHttpHeaders()` (`flutter/lib/common.dart:2769-2773`). It is sent even when the token is empty (`Bearer ` with nothing after it).

Default headers when the caller passes none: `Content-Type: application/json` (`flutter/lib/utils/http_service.dart:18`).

### 1.4 Body, error envelope, status handling

- Request bodies are JSON. Responses are decoded as UTF-8 with malformed bytes tolerated (`flutter/lib/common.dart:4256-4265`).
- Error envelope everywhere: a JSON object with key **`error`** (string). Most Dart callers test `error` **before** the status code, so `200 {"error":"x"}` is an error.
- Most Dart callers require **exactly 200**; 201/204 are failures (see each endpoint).
- JSON types are enforced by Dart field types. A number where a `String` is declared, or `null` where an `int` is declared, throws and the whole response is discarded. Each endpoint lists the types.

### 1.5 Pagination

Used by `/api/ab/shared/profiles`, `/api/ab/peers`, `/api/users`, `/api/peers`, `/api/device-group/accessible`.

- Request query: `current` (1-based page number, string) and `pageSize` (always `100`).
- Response: `{"total": <int>, "data": [ ... ]}`.
- `data` is only read **if the key `total` is present** (`flutter/lib/models/ab_model.dart:333-349`, `1465-1481`; `flutter/lib/models/group_model.dart:132-148`, `197-213`, `256-273`).
- `total` must be a JSON integer. It is taken from the first page only (`if (total == 0) total = json['total']`).
- Loop: request page 1, then keep going `while (current * pageSize < total)`. `total = 0` → exactly one request.
- Items are de-duplicated client-side (by `name` for profiles/users/device groups, by `id` for peers), so returning everything on page 1 with a small `total` works.

### 1.6 Identifiers

- `id`: the RustDesk device ID string of the calling device.
- `uuid`: standard base64 of the machine UUID bytes, `encode64(hbb_common::get_uuid())` (`src/ui_interface.rs:884-886`, `src/common.rs:2147-2150`).
- `session_id`: a random **u64** (`src/client.rs:2977`). It is sent as a bare JSON number and can exceed 2^53 — parse as u64/decimal, not float.
- `conn_id`: i32, per-process incoming connection counter.

### 1.7 Request shapes that trip up frameworks

- Five read endpoints are `POST` with `Content-Type: application/json`, `Content-Length: 0` and **no body**: `/api/ab/personal`, `/api/ab/settings`, `/api/ab/shared/profiles`, `/api/ab/peers`, `/api/ab/tags/{guid}` (`flutter/lib/models/ab_model.dart:234-237`, `2029-2031`). Do not run a JSON body parser on them.
- Two `DELETE`s carry a JSON array body (5.11, 5.15).
- `GET /api/ab` and `GET /api/audit/conn/active` arrive with a `Content-Type: application/json` header and no body.
- Top-level response types vary: object (most), array (`login-options`, `ab/tags`), bare JSON string (`audit/conn/active`), plain text (`sysinfo`, `sysinfo_ver`), empty (AB mutations, audit).
- Unknown request fields must be tolerated (peer objects carry extra keys, audit records gain fields between releases).

---

## 2. Endpoint index

| # | Method + path | Auth | Caller | Needed for |
|---|---|---|---|---|
| 3.1 | `GET` (+`HEAD`) `/api/login-options` | none | UI, core | login dialog |
| 3.2 | `POST /api/login` | none | UI | login |
| 3.3 | `POST /api/currentUser` | Bearer | UI | startup session check |
| 3.4 | `POST /api/logout` | Bearer | UI | logout |
| 4.1 | `POST /api/oidc/auth` | none | core | OIDC login |
| 4.2 | `GET /api/oidc/auth-query` | none | core | OIDC login |
| 5.2 | `GET /api/ab` | Bearer | UI | legacy address book |
| 5.3 | `POST /api/ab` | Bearer | UI | legacy address book |
| 5.4 | `POST /api/ab/personal` | Bearer | UI | mode switch + personal AB |
| 5.5 | `POST /api/ab/settings` | Bearer | UI | AB size limit |
| 5.6 | `POST /api/ab/shared/profiles` | Bearer | UI | shared ABs |
| 5.7 | `POST /api/ab/peers` | Bearer | UI | AB content |
| 5.8 | `POST /api/ab/tags/{guid}` | Bearer | UI | AB tags |
| 5.9 | `POST /api/ab/peer/add/{guid}` | Bearer | UI | AB edit |
| 5.10 | `PUT /api/ab/peer/update/{guid}` | Bearer | UI | AB edit, password sync |
| 5.11 | `DELETE /api/ab/peer/{guid}` | Bearer | UI | AB edit |
| 5.12 | `POST /api/ab/tag/add/{guid}` | Bearer | UI | tags |
| 5.13 | `PUT /api/ab/tag/rename/{guid}` | Bearer | UI | tags |
| 5.14 | `PUT /api/ab/tag/update/{guid}` | Bearer | UI | tags |
| 5.15 | `DELETE /api/ab/tag/{guid}` | Bearer | UI | tags |
| 6.1 | `GET /api/device-group/accessible` | Bearer | UI | group tab (optional) |
| 6.2 | `GET /api/users` | Bearer | UI | group tab |
| 6.3 | `GET /api/peers` | Bearer | UI | group tab |
| 7.1 | `POST /api/heartbeat` | none | core service | device liveness, strategy |
| 7.2 | `POST /api/sysinfo` | none | core service | device inventory |
| 7.3 | `POST /api/sysinfo_ver` | none | core service | dead code in 1.5.3 (section 7.3) |
| 8.1 | `POST /api/audit/conn` | none | core | connection log |
| 8.2 | `POST /api/audit/file` | none | core | file-transfer log |
| 8.3 | `POST /api/audit/alarm` | none | core | security alarms |
| 8.4 | `GET /api/audit/conn/active` | Bearer | UI | end-of-session note |
| 8.5 | `PUT /api/audit` | Bearer | UI | end-of-session note |
| 9 | `POST /api/record` | none | core | never called in 1.5.3 |
| 10.1 | `POST /api/switch-grant` | device signature | core | Pro extra |
| 10.2 | `POST /api/devices/cli` | Bearer (API token) | CLI | Pro extra |
| 10.3 | `POST /api/devices/deploy` | Bearer (API token) | CLI/UI | Pro extra |

---

## 3. Account

### 3.1 `GET /api/login-options`

- **When**: every time the login dialog opens, and on its Retry button (`flutter/lib/common/widgets/login.dart:740-753`, `flutter/lib/models/user_model.dart:240-264`). Plus `HEAD` probes from the core (section 1.2).
- **Auth**: none. Header `Content-Type: application/json` only.
- **Request**: no query, no body.
- **Response**: status must be 2xx, body a **JSON array of strings** (`user_model.dart:244-254`).
  - If any element starts with `common-oidc/`, the remainder of that string is parsed as JSON and returned as the option list: an array of objects `{"name": <string>, "icon": <string, optional SVG markup>}` (`user_model.dart:255-259`, `login.dart:906-908`).
  - Otherwise elements starting with `oidc/` become `{"name": "<rest>"}` (`user_model.dart:260-263`).
  - `[]` → no third-party buttons, password form only.
- **On failure** (non-2xx, non-array, element not a string): an error block with a Retry button is rendered below the password form; password login still works (`login.dart:859-888`).
- `name` is what is later sent as `op` to `/api/oidc/auth`. Built-in icons exist for `github, gitlab, google, apple, okta, facebook, azure, auth0, microsoft` (`login.dart:17-27`).

```http
GET /api/login-options
→ 200
["common-oidc/[{\"name\":\"google\"},{\"name\":\"corp\",\"icon\":\"<svg ...>\"}]","oidc/google","oidc/corp"]
```
Minimal server: `200 []`.

### 3.2 `POST /api/login`

- **When**: user submits the login dialog; again when submitting the verification-code dialog (`login.dart:826-857`, `1012-1045`).
- **Auth**: none. Header `Content-Type: application/json`.
- **Request body** (`flutter/lib/common/hbbs/hbbs.dart:133-178`); every key is omitted when null except `deviceInfo`:

| Field | Type | Password login | Code step |
|---|---|---|---|
| `username` | string | yes | `user.name` from the first response |
| `password` | string | yes | absent |
| `id` | string | device ID | same |
| `uuid` | string | base64 uuid | same |
| `autoLogin` | bool | `true` | `true` |
| `type` | string | `"account"` | `"email_code"` (for both e-mail code and TOTP) |
| `verificationCode` | string | absent | the code |
| `tfaCode` | string | absent | the code, only when the step is TOTP |
| `secret` | string | absent | `secret` from the first response |
| `deviceInfo` | object | `{"os": "linux"\|"windows"\|"macos"\|"android"\|"ios", "type": "client", "name": "<hostname>"}` (`src/ui_interface.rs:1379-1386`); the web client sends `os` = user-agent string, `type` = `"Web client"` (`flutter/lib/web/bridge.dart:797-807`) | same |

- **Response handling** (`user_model.dart:193-235`, `login.dart:773-824`):
  1. Body must be a JSON object; otherwise the dialog shows `Unknown Error: ...` (and a toast `HTTP <code>` when status ≠ 200).
  2. Status ≠ 200 → error text = `body.error` (must be a string; missing → empty message).
  3. Status 200 and `error` present → error text = `body.error`.
  4. Otherwise parsed as `LoginResponse` (`hbbs.dart:180-197`):

| Field | Type | Meaning |
|---|---|---|
| `type` | string | `"access_token"` → done. `"email_check"` → show code dialog. Anything else → `Failed, bad response from server` |
| `access_token` | string | required when `type` = `access_token` |
| `user` | object (UserPayload, 3.5) | required for the UI to become "logged in" — see below |
| `tfa_type` | string | only with `type` = `email_check`: absent or `"email_check"` → e-mail code; `"tfa_check"` → TOTP; anything else → `Failed, bad tfa type from server` |
| `secret` | string | opaque, echoed back in the code step |

  - On `access_token`: token saved to local option `access_token`, `user` saved to local option `user_info` as `{"name","display_name","avatar","status"}` (`login.dart:776-783`, `hbbs.dart:50-62`).
  - The UI treats the user as logged in only when `user.name` is a non-empty string (`user_model.dart:26`, `150-155`, `228-232`). A response without `user` stores the token but leaves the UI logged out until restart.
  - A TOTP challenge must be sent as `type: "email_check"` + `tfa_type: "tfa_check"`; a top-level `type: "tfa_check"` is rejected (`login.dart:790-822`).
- After success the client immediately pulls the address book and the group tab (`login.dart:996-998`).

```http
POST /api/login
{"username":"alice","password":"secret","id":"123456789","uuid":"QUJDRA==","autoLogin":true,"type":"account","deviceInfo":{"os":"macos","type":"client","name":"alice-mbp"}}
→ 200
{"type":"access_token","access_token":"tok_abc","user":{"name":"alice","display_name":"Alice","avatar":"","email":"a@example.com","note":"","status":1,"is_admin":false,"info":{}}}

→ 401 (or 400, any non-200)
{"error":"Wrong credentials"}
```

### 3.3 `POST /api/currentUser`

- **When**: once at app start (desktop `flutter/lib/main.dart:142-143`, mobile `182-183`), on the Retry button of the network-error panel (`flutter/lib/common.dart:4078-4080`), and when another window asks for a refresh (`flutter/lib/desktop/pages/desktop_home_page.dart:785-786`). Skipped when no token is stored (`user_model.dart:57-61`). Not polled.
- **Auth**: `Authorization: Bearer <token>`, `Content-Type: application/json` (`user_model.dart:73-78`).
- **Request body**: `{"id": "<device id>", "uuid": "<base64 uuid>"}`.
- **Response handling** (`user_model.dart:84-113`):
  - Status **401 or 400** → token and `user_info` are wiped (logged out). On 401 the cached address book and group data are wiped too.
  - Else the body must be a JSON object. `error` present → shown in the address-book and group tabs as a server error with Retry; the user stays logged in from cache but **no AB/group pull happens** while this error is set (`ab_model.dart:124`, `group_model.dart:42`).
  - Non-JSON body, transport error → same panel plus a "check your network" hint.
  - Otherwise the object is a UserPayload (3.5).
- After any outcome the client attempts the AB and group pulls (`user_model.dart:110-113`).

```http
POST /api/currentUser
Authorization: Bearer tok_abc
{"id":"123456789","uuid":"QUJDRA=="}
→ 200
{"name":"alice","display_name":"Alice","avatar":"","email":"a@example.com","note":"","status":1,"is_admin":false}
→ 401 {"error":"Invalid token"}
```

### 3.4 `POST /api/logout`

- **When**: user confirms logout; API server changed in settings.
- **Auth**: Bearer. Body `{"id","uuid"}`. Client timeout **2 s** (`user_model.dart:170-190`).
- **Response**: ignored entirely, including errors. Local state is always cleared.

### 3.5 UserPayload (the `user` object)

Dart parser, used for `/api/login`, `/api/currentUser`, `/api/users` (`hbbs.dart:26-48`):

| Field | Type | Required | Use |
|---|---|---|---|
| `name` | string | effectively yes | login name; non-empty = logged in; key for group-tab filtering |
| `display_name` | string | no | shown instead of `name` when non-blank |
| `avatar` | string | no | `data:image/...;base64,...`, absolute `http(s)://` URL, or a path starting with `/` (resolved against the API base, fetched with a plain GET, no auth) (`flutter/lib/common.dart:4294-4328`, `src/ui_interface.rs:263-272`) |
| `email` | string | no | shown read-only in the e-mail code dialog |
| `note` | string | no | parsed, not displayed |
| `verifier` | string or null | no | stored on web only; verification is disabled (`src/common.rs:2713-2715`) |
| `status` | int | no | `0` = disabled, `-1` = unverified, anything else = normal. Only stored; nothing in the UI is gated on it |
| `is_admin` | bool | no | must be literal `true` to count; stored, not used for gating |

Fields `info` and `third_auth_type` are **not** read by Dart. They are read by the Rust OIDC parser (section 4.2), where `info` is mandatory.

String fields must be JSON strings or absent/null; a number there throws.

---

## 4. OIDC device flow

Driven by the Rust core (`src/hbbs_http/account.rs`); the UI polls the core's in-memory state once per second and opens the returned URL in the system browser (`login.dart:289-295`, `392-452`).

### 4.1 `POST /api/oidc/auth`

- **When**: user clicks a provider button.
- **Auth**: none. `Content-Type: application/json`.
- **Request body** (`account.rs:167-174`):

```json
{"op":"google","id":"123456789","uuid":"QUJDRA==","deviceInfo":{"os":"macos","type":"client","name":"alice-mbp"},"apiDomain":"https://rd.example.com"}
```
- **Response** — status is ignored, only the body counts (`account.rs:175-176`, `src/hbbs_http.rs:24-39`):
  - Not a JSON object → flow fails with the parse error text.
  - `{"error": "<string>"}` → flow fails, message shown.
  - Else must deserialize to `{"code": <string>, "url": <absolute URL string>}` (`account.rs:24-28`). Missing field or unparsable URL → `Invalid auth response`.

```json
{"code":"dev_7f3c","url":"https://rd.example.com/oidc/start?state=dev_7f3c"}
```
The client opens `url` in the external browser and shows it for manual copy.

### 4.2 `GET /api/oidc/auth-query?code=<code>&id=<id>&uuid=<uuid>`

- **When**: immediately after 4.1, then **every 1.0 s** (sleep between requests) for at most **180 s** (`account.rs:17-18`, `291-347`). Stops on success, on a terminal error, on user cancel, or at timeout (`"timeout"` shown).
- **Auth**: none, no headers (`account.rs:195-196`).
- **Response body** (status ignored):

| Body | Client behaviour |
|---|---|
| `{"error": "...No authed oidc is found..."}` (substring match, case-sensitive) | keep polling (`account.rs:326-328`) |
| `{"error": "<any other string>"}` | stop, show the string |
| `{"error": <non-string>}` | ignored, keep polling |
| object that fails to deserialize as AuthBody | **ignored, keep polling until the 180 s timeout** (`account.rs:334-336`) |
| not a JSON object / transport error | ignored, keep polling |
| valid AuthBody | success |

AuthBody (`account.rs:99-108`) and its Rust-side UserPayload (`account.rs:79-97`):

| Field | Type | Required |
|---|---|---|
| `access_token` | string | **yes** |
| `type` | string | **yes**; token is persisted only when it equals `"access_token"` |
| `tfa_type` | string | no (default `""`) |
| `secret` | string | no (default `""`) |
| `user.name` | string | **yes** |
| `user.info` | **object** | **yes** — `{}` is fine; `null` or absent fails. Optional members: `email_verification` bool, `email_alarm_notification` bool, `login_device_whitelist` array of `{"data": string, "info": {"os","type","name"}, "exp": u64}`, `other` object of string→string (`account.rs:53-69`) |
| `user.status` | int | no (default 1); if present must be exactly `0`, `1` or `-1` (`account.rs:71-77`) |
| `user.display_name`, `user.avatar`, `user.email`, `user.note`, `user.third_auth_type` | string or null | no |
| `user.is_admin` | bool | no |

On success the core stores `access_token` and `user_info = {"name","display_name","avatar","status"}` itself (`account.rs:304-321`), then hands the body to the UI, which applies it like a login response (`login.dart:913-928`).

```http
GET /api/oidc/auth-query?code=dev_7f3c&id=123456789&uuid=QUJDRA%3D%3D
→ 200 {"error":"No authed oidc is found"}            (pending)
→ 200 {"access_token":"tok_abc","type":"access_token","user":{"name":"alice","status":1,"is_admin":false,"info":{}}}
```

The web client runs this flow in JavaScript that is not part of the repo (`flutter/lib/web/bridge.dart:1640-1659`); not verified.

---

## 5. Address book

### 5.1 Mode decision: legacy vs personal/shared

Run on every "list" pull: at startup after `currentUser`, after login, and on the refresh button of the address-book tab (`user_model.dart:163-168`, `flutter/lib/common/widgets/peer_tab_page.dart:61-63`, `223`). Sequence (`ab_model.dart:142-218`):

1. `POST /api/ab/personal`.
   - **404** → legacy mode (`ab_model.dart:271-275`).
   - `200` object **without** `guid` (e.g. `{}`) → legacy mode as well (`ab_model.dart:284`, `154`).
   - `200` object with string `guid` → personal/shared mode.
   - Anything else (`{"error":..}`, other status, non-object body such as `null`) → pull aborted with an error banner; no address book is shown (`ab_model.dart:276-292`, `151-153`).
2. Personal/shared mode only, and only while the cached limit is 0: `POST /api/ab/settings`.
3. Personal/shared mode only: `POST /api/ab/shared/profiles` (paged).
4. Pull the currently selected book; if that is not the personal one and the personal one has never been loaded, pull it too (`ab_model.dart:193-201`).

Books are keyed **by name** on the client. The personal book is always named `My address book`, the legacy one `Legacy address book` (`ab_model.dart:32-33`). A shared profile with one of those names replaces the built-in entry (`ab_model.dart:340-345`) — the server must not return such names, and must not list the personal book in `shared/profiles`.

401 on any of the list calls logs the user out (`ab_model.dart:225-227`).

After every mutation in personal/shared mode the client re-pulls peers and tags of the touched book (`ab_model.dart:790-801`).

### 5.2 Legacy: `GET /api/ab`

- **When**: legacy mode pull (startup, login, refresh button).
- **Headers**: Bearer, `Content-Type: application/json`, `Accept-Encoding: gzip` (`ab_model.dart:1013-1016`). No body.
- **Response** (`ab_model.dart:1017-1052`):
  - body `null` (any case) → empty book, but the book stays "not initialised": recent-session sync and caching stay off until a pull returns `data` (`ab_model.dart:91`, `215-217`).
  - empty body → nothing happens, not initialised.
  - JSON object with `error` → banner.
  - JSON object with `data` → **`data` is a string containing JSON** (double encoding). Optional `licensed_devices` (int; > 0 caps the peer count, extra peers are dropped client-side, `ab_model.dart:1339-1341`).
- Decoded `data` (`ab_model.dart:1325-1357`):

```json
{
  "tags": ["office", "lab"],
  "peers": [
    {"id":"123456789","hash":"<base64>","username":"bob","hostname":"bob-pc","platform":"Windows","alias":"Bob","tags":["office"]}
  ],
  "tag_colors": "{\"office\":4288585374,\"lab\":4278238420}"
}
```
`tag_colors` is itself a **JSON string** (third level of encoding) mapping tag → 32-bit ARGB int. Peers use the Peer shape of 5.7.

Full wire example:
```http
GET /api/ab
→ 200
{"licensed_devices":0,"data":"{\"tags\":[\"office\"],\"peers\":[{\"id\":\"123456789\",\"hash\":\"\",\"username\":\"bob\",\"hostname\":\"bob-pc\",\"platform\":\"Windows\",\"alias\":\"Bob\",\"tags\":[\"office\"]}],\"tag_colors\":\"{\\\"office\\\":4288585374}\"}"}
```
Recommended for an empty book: `{"data":"{\"tags\":[],\"peers\":[],\"tag_colors\":\"{}\"}"}` rather than `null`, so the book becomes initialised.

401 → logged out (only when an error banner is being shown, `ab_model.dart:1046-1050`).

### 5.3 Legacy: `POST /api/ab`

- **When**: every edit in legacy mode (the whole book is re-uploaded), and automatic sync from recent sessions — checked every 3 s, pushed only on change (`ab_model.dart:87-94`, `1173-1204`).
- **Headers**: Bearer, `Content-Type: application/json`.
- **Request body** (`ab_model.dart:1067`, `1309-1323`): `{"data": "<JSON string>"}` where the string decodes to `{"tags":[...], "peers":[{"id","username","hostname","platform","alias","tags","hash"}], "tag_colors":"<JSON string>"}` (`flutter/lib/models/peer_model.dart:70-83`).
- **Response** (`ab_model.dart:1068-1087`): success = status 200 with empty body or `null`, or status 200 with a JSON **object** that has no `error`. Anything else → red toast.

The legacy Sciter UI (non-Flutter builds) uses `POST /api/ab/get` with body `{}` instead of `GET /api/ab`, expecting `{"data": "<JSON string>", "updated_at": ...}` (`src/ui/ab.tis:646-666`).

### 5.4 `POST /api/ab/personal`

- **Headers**: Bearer, `Content-Type: application/json`, `Content-Length: 0`. **No body** (`ab_model.dart:266-269`, `2029-2031`).
- **Response**: `{"guid": "<string>"}`. Only `guid` is read; it must be a string. See 5.1 for the other outcomes.

```http
POST /api/ab/personal → 200 {"guid":"018f6b1e-personal"}
POST /api/ab/personal → 404            (legacy mode)
```

### 5.5 `POST /api/ab/settings`

- No body, same headers as 5.4 (`ab_model.dart:230-258`).
- **Response**: `{"max_peer_one_ab": <int>}`; `0` or absent = unlimited (and the call is repeated on the next list pull). When > 0 the client refuses to add peers once a book holds that many (`ab_model.dart:1386-1390`).
- 404 → ignored. `error` or other non-200 → banner.

### 5.6 `POST /api/ab/shared/profiles?current=N&pageSize=100`

- No body (`ab_model.dart:295-357`). Paged (1.5).
- 404 → ignored (no shared books).
- `data` items — AbProfile (`hbbs.dart:258-275`):

| Field | Type | Notes |
|---|---|---|
| `guid` | string | used in all per-book paths |
| `name` | string | client-side key; must be unique, not `My address book` / `Legacy address book` |
| `owner` | string | display |
| `note` | string | display |
| `rule` | int | **1** = read, **2** = read/write, **3** = full control (`hbbs.dart:210-213`) |
| `info` | object or null | optional `{"password": "<string>"}` = default shared password for every peer of this book (`ab_model.dart:843-863`) |

Client-side permission use: every editing action (add/rename/tag/note/set shared password/**delete** peers and tags) is offered when `rule` is 2 or 3 (`ab_model.dart:1392-1400`, `flutter/lib/common/widgets/peer_card.dart:1207-1233`). `rule` 3 has no extra client-side effect in 1.5.3 (`fullControl()` is defined but unused). The personal book is always writable.

```json
{"total":1,"data":[{"guid":"018f6b1e-team","name":"Team","owner":"alice","note":"","rule":2,"info":{"password":"shared-default"}}]}
```

### 5.7 `POST /api/ab/peers?current=N&pageSize=100&ab=<guid>`

- No body (`ab_model.dart:1432-1497`). Paged. Called for the personal guid and for shared guids alike.
- No 404 special case: any non-200 or `error` is a pull error. 401 → logged out.
- `data` items — Peer (`peer_model.dart:33-48`):

| Field | Type | Notes |
|---|---|---|
| `id` | string | device ID |
| `hash` | string | personal book only: base64 of the remembered password hash; the connecting client uses it instead of asking (`src/client.rs:4805-4827`) |
| `password` | string | shared book only: **plaintext** shared password, used automatically on connect (`peer_card.dart:1586-1598`) |
| `username` | string | OS user of the device |
| `hostname` | string | |
| `platform` | string | `Windows`, `Linux`, `Mac OS`, `Android` (`flutter/lib/consts.dart:37-41`) |
| `alias` | string | |
| `tags` | array of strings | `null`/absent → `[]` |
| `note` | string | non-string is treated as empty |
| `forceAlwaysRelay` | string | only the literal string `"true"` counts |
| `rdpPort`, `rdpUsername` | string | |
| `loginName` | string | |
| `device_group_name` | string | |
| `same_server` | bool or null | `true` stops the client from overwriting username/hostname/platform during sync (`ab_model.dart:1692-1709`) |

All optional; wrong JSON types throw.

```json
{"total":2,"data":[
 {"id":"123456789","hash":"","password":"","username":"bob","hostname":"bob-pc","platform":"Windows","alias":"Bob","tags":["office"],"note":"front desk"},
 {"id":"987654321","username":"root","hostname":"srv1","platform":"Linux","alias":"","tags":[]}
]}
```

### 5.8 `POST /api/ab/tags/{guid}`

- No body (`ab_model.dart:1499-1544`).
- **Response**: a bare **JSON array** (no envelope, no paging) of `{"name": <string>, "color": <int>}`. `color` is **required and must be an int** (32-bit ARGB, e.g. `4288585374`); absent/null throws (`hbbs.dart:277-286`).
- Status must be 200. An error object is not an array and surfaces as a pull error.

```json
[{"name":"office","color":4288585374},{"name":"lab","color":4278238420}]
```

### 5.9–5.15 Mutations: common response rule

`_jsonDecodeActionResp` (`ab_model.dart:2002-2026`): **success is status 200 with an empty body, nothing else.**
`200 {}`, `200 null`, `200 "ok"`, `204` and `201` are all reported as errors (`{}` yields the toast text `null`). Failure text shown to the user = `error` from a JSON object body, else `HTTP <code>, <body>`.

All use headers Bearer + `Content-Type: application/json`.

| # | Call | Request body | Source |
|---|---|---|---|
| 5.9 | `POST /api/ab/peer/add/{guid}` | one peer object per request: at least `{"id","alias","tags"}`; may carry `password` (shared books only), `note`, `hash` (personal only), and when copied from another tab the full Peer JSON: `username, hostname, platform, forceAlwaysRelay` (string `"true"`/`"false"`), `rdpPort, rdpUsername, loginName, device_group_name, note, same_server` (bool or null). `hash` is stripped for shared books, `password` for the personal book. Tags not present in the target book are removed first. | `ab_model.dart:375-417`, `1547-1578`, `flutter/lib/common/widgets/dialog.dart:2481-2489`, `peer_model.dart:50-68` |
| 5.10 | `PUT /api/ab/peer/update/{guid}` | partial update keyed by `id`; exactly one of: `{"id","tags":[...]}`, `{"id","alias"}`, `{"id","note"}`, `{"id","hash"}` (personal; `""` = forget password), `{"id","password"}` (shared), or a sync patch `{"id", "username"?, "hostname"?, "platform"?, "hash"?}` | `ab_model.dart:1580-1749` |
| 5.11 | `DELETE /api/ab/peer/{guid}` | JSON array of ids: `["123456789","987654321"]` (**DELETE with a body**) | `ab_model.dart:1751-1771` |
| 5.12 | `POST /api/ab/tag/add/{guid}` | `{"name":"office","color":4288585374}`, one request per tag | `ab_model.dart:1775-1802` |
| 5.13 | `PUT /api/ab/tag/rename/{guid}` | `{"old":"office","new":"hq"}` (server must rename on peers too) | `ab_model.dart:1804-1831` |
| 5.14 | `PUT /api/ab/tag/update/{guid}` | `{"name":"office","color":4278238420}` | `ab_model.dart:1833-1855` |
| 5.15 | `DELETE /api/ab/tag/{guid}` | JSON array with one tag name: `["office"]` | `ab_model.dart:1857-1876` |

Frequency: user-driven, except the 5.10 sync patch. Every 3 s the main window checks recent sessions and, for peers already in the current writable book, PUTs changed `username`/`hostname`/`platform`/`hash` (`ab_model.dart:87-94`, `539-592`, `1681-1749`). It only fires when something differs and local option `sync-ab-with-recent-sessions` is `Y`. `hash` is also pushed right after a successful connection with a remembered password (`flutter/lib/models/model.dart:433-441`).

Shared-password summary: per-peer `password` (plaintext) set via 5.10; fallback `info.password` on the profile (5.6); neither applies to the personal book, which uses `hash`.

---

## 6. Group / "accessible devices" tab

Pulled together with the address book: at startup, after login, on the tab's refresh button and after some peer-card actions (`user_model.dart:163-168`, `peer_tab_page.dart:68`, `peer_card.dart:1359`). Order: device groups → users → peers (`group_model.dart:64-101`). If users or peers fail, nothing is replaced. Status 401 on the last request logs the user out (`group_model.dart:57-58`). An error text containing `disabled` clears the lists (`group_model.dart:369-377`).

All three are `GET` with only the Bearer header.

### 6.1 `GET /api/device-group/accessible?current=N&pageSize=100`

- Paged; items `{"name": <string>}` (`hbbs.dart:288-294`).
- **Every failure is swallowed** (404, error body, non-JSON): the tab simply shows no device groups (`group_model.dart:151-157`).

### 6.2 `GET /api/users?current=N&pageSize=100&accessible=&status=1`

- `accessible` is sent as an empty value; `status=1` asks for normal users only (`group_model.dart:174-179`).
- Paged; items are UserPayload (3.5); only `name` and `display_name` matter. The logged-in user is moved to the top.
- Errors → banner `pull_group_failed_tip: ...`. The strings `Admin required!` and `...ambiguous column name: status...` are mapped to an "upgrade server" hint (`group_model.dart:184-193`).

```json
{"total":2,"data":[{"name":"alice","display_name":"Alice","status":1,"is_admin":true},{"name":"bob","status":1}]}
```

### 6.3 `GET /api/peers?current=N&pageSize=100&accessible=&status=1`

- Paged; items — PeerPayload (`hbbs.dart:77-105`):

| Field | Type | Use |
|---|---|---|
| `id` | string | device ID |
| `info` | object | `username` (string), `os` (string), `device_name` (string); a non-object is treated as `{}` |
| `status` | int or null | parsed, unused |
| `user` | string | parsed, unused |
| `user_name` | string | owner's login name; a peer is listed under user X when `user_name == X.name` (`flutter/lib/common/widgets/peers_view.dart:575`, `591`) |
| `device_group_name` | string | listed under device group with that `name` |
| `note` | string | shown on the card |

- `info.os` → platform: first token before `" / "`, lower-cased, must be `windows`/`linux`/`macos`/`android`; otherwise `Linux` if the string contains `linux`; else unknown (`hbbs.dart:107-130`). Passing through the `os` string from `/api/sysinfo` works.

```json
{"total":1,"data":[{"id":"123456789","info":{"username":"bob","os":"windows / Windows 11 Pro - 10.0.22631","device_name":"bob-pc"},"status":1,"user":"u1","user_name":"bob","device_group_name":"HQ","note":""}]}
```

---

## 7. Device → server (service process, no token)

Loop in `src/hbbs_http/sync.rs:87-282`, started with the rendezvous mediator (`src/rendezvous_mediator.rs:319`), i.e. in the process that accepts incoming connections; not started for outgoing-only builds or iOS. Tick every **3 s**. Nothing is sent when the API server is empty or a `*.rustdesk.com` host (`sync.rs:284-293`), or when option `stop-service` is on (`sync.rs:104-106`).

All three are `POST` with `Content-Type: application/json` and **no Authorization**. The HTTP status is ignored (section 1.2).

### 7.1 `POST /api/heartbeat`

- **When**: every tick (3 s) while at least one incoming connection is alive, otherwise every **15 s** (`sync.rs:18`, `232-235`).
- **Request** (`sync.rs:236-245`):

```json
{"id":"123456789","uuid":"QUJDRA==","ver":1005030,"conns":[12,13],"modified_at":0}
```
| Field | Type | Notes |
|---|---|---|
| `id` | string | device ID |
| `uuid` | string | base64 uuid |
| `ver` | int | version number; 1.5.3 → `1005030` (`hbb_common/src/lib.rs:264-289`) |
| `conns` | array of int | alive incoming connection ids; **omitted when empty** |
| `modified_at` | int | the strategy timestamp the client last stored (0 initially) |

- **Response**: parsed only if it is a JSON object; otherwise ignored (`sync.rs:249-278`). All keys optional:

| Key | Type | Effect |
|---|---|---|
| `sysinfo` | anything | presence forces a new `/api/sysinfo` upload |
| `disconnect` | array of int | those `conn_id`s are closed with "Closed manually by web console" (`src/server/connection.rs:1072-1077`) |
| `strategy` | `{"config_options": {"<option>": "<string value>"}, "extra": {...}}` | options are written to the device config; empty value removes the option. Security-sensitive keys are dropped (`sync.rs:295-341`). If applying fails, `modified_at` of that response is skipped |
| `modified_at` | int | stored and echoed in later heartbeats |

Send `strategy` only when the client's `modified_at` differs from the server's. Minimal server: `200 {}`.

### 7.2 `POST /api/sysinfo`

- **When**: on the first tick after service start, again when the device ID, API URL or OS username changes, and when a heartbeat response contains `sysinfo`. After a failed upload it is retried no sooner than **120 s** later; after `ID_NOT_FOUND` on the next tick (`sync.rs:108-131`, `211-230`).
- **Request** (`src/common.rs:962-1006`, `sync.rs:133-180`):

```json
{"id":"123456789","uuid":"QUJDRA==","version":"1.5.3","cpu":"Apple M3, 4.05GHz, 12/12 cores","memory":"36GB","os":"macos / macOS 15.1","hostname":"alice-mbp","username":"alice"}
```
`username` is absent on mobile and when no user is logged in. Options `preset-device-username` / `preset-device-name`, when set, replace `username` / `hostname`. Optional provisioning keys, present only when configured: `preset-address-book-name`, `preset-address-book-tag`, `preset-address-book-alias`, `preset-address-book-password`, `preset-address-book-note`, `preset-user-name`, `preset-strategy-name`, `preset-device-group-name`, `preset-note` (`libs/base/src/config/keys.rs:79-86`, `114-116`).

- **Response**: **plain text**, compared literally:
  - `SYSINFO_UPDATED` → done; the client also marks the server as "Pro" (`sync.rs:213-220`, `387-391`).
  - `ID_NOT_FOUND` → retry on the next tick.
  - anything else → retry after 120 s, forever.

### 7.3 `POST /api/sysinfo_ver`

- Empty body. Expected response: an opaque version string; the client would skip the sysinfo upload when its cached hash and this string are unchanged (`sync.rs:181-209`).
- In 1.5.3 the whole block is guarded by `is_public(url)`, and the loop never runs with a public URL, so **this endpoint is never called by a 1.5.3 client talking to a self-hosted server** (`sync.rs:182`, `284-293`). Older clients do call it; returning any constant string is enough.

---

## 8. Audit

All `POST` audit calls come from the Rust core with `Content-Type: application/json`, **no Authorization**, and only when the API server is non-empty and non-public (`src/common.rs:1373-1379`).

Delivery rule (`src/server/connection.rs:1670-1767`):

- **Success = status 2xx with an empty (whitespace-only) body.**
- 2xx with **any** non-empty body (including `{}` or `{"code":0}`) is treated as failure and retried.
- 5xx, 408, 429, transport errors: retried. Other 4xx (incl. 404): dropped immediately, logged.
- Up to 3 attempts, waiting 10 s then 30 s, total window 120 s.
- Every record carries **`nonce`** (UUID v4 string); the same nonce is re-sent on retries, so the server should de-duplicate on it for at least five minutes.

### 8.1 `POST /api/audit/conn`

Sent by the **controlled** device, sequentially per connection (`connection.rs:1331-1336`). Fields added to every record (`connection.rs:1559-1572`): `id` (controlled device ID), `uuid`, `conn_id` (int), `session_id` (u64), `nonce`.

| Event | When | Extra fields | Source |
|---|---|---|---|
| open | TCP connection accepted, before authentication | `"action":"new"`, `"ip":"<peer ip>"`, optional `conn_audit_ref` (string from hbbs Pro). `session_id` is still `0` | `connection.rs:1523-1530` |
| authorized | login accepted | **no `action` key**; `"peer":["<controller id>","<controller name>"]`, `"type"`: 0 remote, 1 file transfer, 2 port forward, 3 view camera, 4 terminal; optional `"primary_auth"`: 1 click-accept, 2 one-time password, 3 permanent password, 4 switch sides; optional `"two_factor"`: 1 TOTP, 2 trusted device | `connection.rs:1917-1948`, `312-332` |
| close | connection loop ends | `"action":"close"` | `connection.rs:1240-1242` |

```json
{"action":"new","ip":"203.0.113.7","id":"123456789","uuid":"QUJDRA==","conn_id":12,"session_id":0,"nonce":"6f1c...-uuid"}
{"peer":["987654321","alice"],"type":0,"primary_auth":3,"id":"123456789","uuid":"QUJDRA==","conn_id":12,"session_id":14172034923488123456,"nonce":"..."}
{"action":"close","id":"123456789","uuid":"QUJDRA==","conn_id":12,"session_id":14172034923488123456,"nonce":"..."}
```
Correlate the three by (`id`, `conn_id`).

A fourth shape comes from the **controlling** side when the user writes a session note from the toolbar, only if logged in (`src/ui_session_interface.rs:581-600`, `2111-2115`): `{"id":"<controlled device id>","session_id":<u64>,"note":"<text, ≤256 chars>"}` — no `uuid`, `conn_id`, `nonce`, no retry, response ignored.

### 8.2 `POST /api/audit/file`

One record per transfer request, from the controlled device (`connection.rs:1590-1625`):

```json
{"id":"123456789","uuid":"QUJDRA==","peer_id":"987654321","conn_id":12,"type":1,"path":"C:\\Users\\bob\\Desktop","is_file":false,
 "info":"{\"ip\":\"203.0.113.7\",\"name\":\"alice\",\"num\":2,\"files\":[[\"a.txt\",1024],[\"b.bin\",512]]}","nonce":"..."}
```
| Field | Type | Notes |
|---|---|---|
| `type` | int | 0 = controlled device sends (remote → controller), 1 = controlled device receives (`connection.rs:6558-6561`) |
| `path` | string | directory or file path; `""` for clipboard file copy; `"Remote print"` for print jobs |
| `is_file` | bool | true when exactly one entry with an empty name |
| `info` | **string containing JSON** | `ip`, `name` (controller name), `num` (total file count), `files`: up to 10 largest as `[name, size]` pairs |
| `peer_id` | string | controller's ID |

### 8.3 `POST /api/audit/alarm`

Fire-and-forget security events from the controlled device (`connection.rs:1627-1651`):

```json
{"id":"123456789","uuid":"QUJDRA==","typ":1,"info":"{\"ip\":\"203.0.113.7\",\"id\":\"987654321\",\"name\":\"alice\"}","conn_id":12,"nonce":"..."}
```
`info` is a **string containing JSON**. `typ` (`connection.rs:6543-6556`): 0 IP whitelist, 1 more than 30 failed attempts, 2 six attempts within a minute, 6 IPv6-prefix attempts, 7 terminal OS-login backoff, 8 terminal OS-login concurrency, 9 session-scope violation, 10 ID whitelist. `conn_audit_ref` may be added for types 0 and 10.

### 8.4 `GET /api/audit/conn/active?id=<controlled id>&session_id=<u64>&conn_type=<0-4>`

- **When**: on the controlling side when a session starts, only if logged in and local option `allow-ask-for-note` is on (`flutter/lib/models/model.dart:1254-1339`). Up to 6 attempts with 1,1,2,2,3 s gaps.
- **Auth**: Bearer.
- **Response**: status 200 with a **JSON string** (the audit record's guid) or `null`/`""` (retry). Any non-200 stops the attempts.

```http
→ 200 "018f6b1e-audit-guid"
```

### 8.5 `PUT /api/audit`

- **When**: user enters a note in the end-of-connection dialog (only shown if 8.4 returned a guid).
- **Auth**: Bearer. Body `{"guid":"018f6b1e-audit-guid","note":"text"}` (`flutter/lib/common/widgets/dialog.dart:1662-1693`).
- **Response**: only the status is looked at (200 = ok); failures are logged, not shown. Client timeout 6 s.

---

## 9. `POST /api/record`

Uploader code exists (`src/hbbs_http/record_upload.rs`) but its enable flag is never set anywhere in the 1.5.3 sources (`record_upload.rs:19-25`, `src/server/video_service.rs:1186-1194`), so **a stock 1.5.3 client never calls it**. Contract, for completeness:

- No auth, no JSON. Query string selects the operation; body is raw bytes.
- `?type=new&file=<name>` (empty body) → `?type=part&file=<name>&offset=<n>&length=<n>` (file bytes; sent once ≥ 1 s has passed and ≥ 1 MiB is pending) → `?type=tail&file=<name>&offset=0&length=<n>` (first ≤1024 bytes of the file again, the rewritten header) or `?type=remove&file=<name>`.
- Response: if it parses as a JSON object with `error`, the upload stops; anything else is success (`record_upload.rs:93-115`).

---

## 10. Pro-only extras a small server can skip

| Endpoint | What it is | Client on 404 |
|---|---|---|
| `POST /api/switch-grant` | sent by the controlled desktop when "switch sides" is requested. Body `{"id","switch_code_verifier","timestamp","signature"}` (Ed25519 over `switch-grant\0id\0verifier\0timestamp`). Expects `{"accepted": true}` or `{"accepted": false, "server_time": <unix s>}` (one retry) (`src/hbbs_http/sync.rs:393-462`) | non-JSON → error logged; switch-sides itself continues |
| `POST /api/devices/cli` | `rustdesk --assign --token <api token> ...`. Header `Authorization: Bearer <api token>`. Body `{"id","uuid"}` plus any of `user_name, strategy_name, address_book_name, address_book_tag, address_book_alias, address_book_password, address_book_note, device_group_name, note, device_username, device_name`. Empty response body prints `Done!`, otherwise the body is printed (`src/core_main.rs:530-627`) | body printed to the terminal |
| `POST /api/devices/deploy` | `rustdesk --deploy --token ... [--id ...]`. Body `{"id","uuid","pk"}` (`pk` = base64 public key). Expects `{"result": "OK" \| "NOT_ENABLED" \| "INVALID_INPUT" \| "ID_TAKEN"}` (`src/ui_interface.rs:1133-1200`) | `Unknown response.` / body shown |
| `HttpProxyRequest` over hbbs | section 1.2; lives in the rendezvous server, not the API server | falls back to the plain HTTP result |
| avatar static files | only if `avatar` is a `/path` or URL (3.5) | initials are shown |
| `GET /api/audit/conn/active`, `PUT /api/audit` | 8.4, 8.5 | no end-of-session note dialog |
| `POST /api/audit/alarm` | 8.3 | dropped, logged |
| `/api/oidc/*` | section 4 | error text in the login dialog; unreachable when `login-options` returns `[]` |
| `/api/device-group/accessible` | 6.1 | silently empty |
| `/api/ab/settings`, `/api/ab/shared/profiles` | 5.5, 5.6 | silently skipped |
| `/api/sysinfo_ver`, `/api/record` | 7.3, 9 | not called by 1.5.3 |

Not present anywhere in the 1.5.3 Rust/Dart sources: `/api/software*`, `/api/plugin*`, `/api/server-config*`, `/api/shared-peer`. Software update checks go to `https://api.rustdesk.com/version/latest`, never to the self-hosted server (`src/common.rs:1177-1188`). The Python scripts under `res/` (`users.py`, `devices.py`, `ab.py`, `audits.py`, ...) are admin tools against the Pro REST API, not client code; not analysed.

What happens on 404 for the core endpoints:

| Endpoint | Client on 404 |
|---|---|
| `/api/login-options` | error block + Retry under the password form; password login still works |
| `/api/login` | `Unknown Error` / empty error in the dialog |
| `/api/currentUser` | network-error panel in AB and group tabs; user stays logged in from cache; AB/group not pulled |
| `/api/logout` | ignored |
| `/api/ab/personal` | **legacy mode** |
| `/api/ab` (legacy) | pull error banner |
| `/api/users`, `/api/peers` | banner in the group tab |
| `/api/heartbeat` | ignored, retried every 15 s |
| `/api/sysinfo` | retried every 120 s; "Pro" flag stays off |
| `/api/audit/conn`, `/file` | dropped, logged |

---

## 11. Direct answers

**(1) Token storage, sending, "logged in".**
The token is stored in plain text in the per-user local config under option `access_token`, next to `user_info` (JSON `{"name","display_name","avatar","status"}`) (`flutter/lib/common/widgets/login.dart:779-782`, `src/hbbs_http/account.rs:304-319`, `src/ui_interface.rs:257-259`). It is sent as `Authorization: Bearer <token>` (`flutter/lib/common.dart:2769-2773`). The same token is also passed to the rendezvous and relay servers inside `PunchHoleRequest.token` and `RequestRelay.token` (`src/ui_session_interface.rs:1980`, `src/client.rs:955-957`, `1825-1827`).
"Logged in" in the UI = in-memory `userName` non-empty (`flutter/lib/models/user_model.dart:26`). At startup it is seeded from cached `user_info` if a token exists, then confirmed by `/api/currentUser`; 401/400 clears both. The Rust side only checks that `access_token` is non-empty (`src/ui_session_interface.rs:582`).
There is no refresh-token mechanism and no client-side expiry; the server ends a session by answering 401.

**(2) Endpoints called without a token.**
`GET`/`HEAD /api/login-options`, `POST /api/login`, `POST /api/oidc/auth`, `GET /api/oidc/auth-query`, `POST /api/heartbeat`, `POST /api/sysinfo`, `POST /api/sysinfo_ver`, `POST /api/audit/conn` (including the note variant), `POST /api/audit/file`, `POST /api/audit/alarm`, `POST /api/record`, `POST /api/switch-grant` (device signature instead). The device endpoints identify the caller only by `id` + `uuid` in the body.
`/api/devices/cli` and `/api/devices/deploy` carry an admin API token typed on the command line, not the login token.

**(3) Online state of address-book entries.**
Not from the API server. The client sends an `OnlineRequest` protobuf to the **rendezvous server on TCP port (rendezvous port − 1)**, 21115 by default, and reads a bitmask `OnlineResponse` (`src/client.rs:5369-5440`, `hbb_common/protos/rendezvous.proto:227-234`). Queried on list load and then every 6 s on self-hosted servers (20 s on the public one) for the visible tab (`flutter/lib/common/widgets/peers_view.dart:313-346`). `status` in `/api/peers` is parsed and ignored.

**(4) `user` fields.**
See 3.5 (Dart) and 4.2 (Rust, OIDC only). `status`: `1` normal, `0` disabled, `-1` unverified. Dart maps any other value to normal; Rust accepts only those three. `is_admin`, `email`, `note` are parsed and stored, not acted on. `info` and `third_auth_type` are read only by the Rust OIDC parser, and there `info` must be a JSON object.

---

## 12. lejianwen/rustdesk-api vs the 1.5.3 client

License: MIT, "Copyright (c) 2024-present Lejianwen and contributors" (`lejianwen/LICENSE`).

Where they disagree, the client wins:

| Topic | lejianwen | 1.5.3 client | Effect |
|---|---|---|---|
| Audit success body | `200 {"code":0,"message":"success","data":""}` (`lejianwen/http/controller/api/audit.go:58`, `83`) | success only on empty body (8) | every audit record is re-posted 3 times; no `nonce` field, so rows are duplicated |
| `/api/ab/personal` when personal books are disabled | `200 null` (`lejianwen/http/controller/api/ab.go:354-356`) | only 404 or an object without `guid` selects legacy mode | pull aborts with banner text `null` |
| Paging parameter | binds `page` (`lejianwen/http/request/api/user.go:41-53`) | sends `current` | always page 1; users/peers beyond the first 100 never load |
| `PUT /api/ab/peer/update` | keeps only `password, hash, tags, alias` (`ab.go:710`) | also sends `note`, `username`, `hostname`, `platform` | note edits and sync patches are silently dropped |
| Peer JSON | `sameServer` key, `forceAlwaysRelay` as bool, no `note` (`lejianwen/model/addressBook.go:21-41`) | reads `same_server`, compares `forceAlwaysRelay` to string `"true"`, reads `note` | those fields are always empty/false on the client |
| Delete peer / delete tag | requires rule 3 (`ab.go:306`, `642`) | offers delete at rule 2 | rule-2 users get a `NoAccess` toast |
| `/api/ab/shared/profiles` | `"total": 0` (`ab.go:443-446`) | paged | fine, one request |
| `/api/ab/peers` | always first 1000 rows, real `total` (`ab.go:551-556`) | paged by 100 | redundant requests above 100 peers |
| `session_id` in audit | `float64` (`lejianwen/http/request/api/audit.go:16`) | u64 | precision loss |
| User `status` | 1 enabled / 2 disabled (`lejianwen/model/model.go:10-11`) | 0 / 1 / -1 | Dart shows 2 as normal; Rust OIDC parser would reject 2 |
| User object | `name, email, note, is_admin, status, info:{}` (`lejianwen/http/response/api/user.go:21-37`) | also `display_name`, `avatar` | tolerated |
| Missing endpoints | no `/api/audit/alarm`, `/api/audit/conn/active`, `PUT /api/audit`, `/api/record`, `/api/switch-grant`, `/api/devices/*` (`lejianwen/http/router/api.go`) | section 10 | 404 behaviour as listed |

Where they agree and confirm the contract: `401 {"error":"Unauthorized"}` for bad tokens (`lejianwen/http/middleware/rustauth.go`); login failure as `400 {"error":...}`; mutation endpoints answer `200` with an empty string body (`ab.go:176`, `612`, `660`, `726`); pending OIDC answer `200 {"error":"No authed oidc is found", ...}` (`lejianwen/http/controller/api/ouath.go:84`); `/api/sysinfo` answers text `SYSINFO_UPDATED` (`lejianwen/http/controller/api/peer.go:58`); heartbeat answers `{}`; legacy `GET /api/ab` returns `{"data":"<json string>"}`; `login-options` returns both a `common-oidc/[...]` element and `oidc/<name>` elements; guid is an opaque string (`"<group>-<user>-<collection>"` there).

lejianwen-only routes not used by the official client: `/api/server-config`, `/api/server-config-v2`, `/api/shared-peer`, `/api/user/info`, `/api/version`, `/api/oauth/*`, `/api/oidc/callback|login|msg`.

---

## 13. Not verified

- Nothing was executed; all behaviour is read from source. Dart runtime type errors (e.g. `null` assigned to a non-nullable `Map`) are inferred from the language rules, not observed.
- **Web client**: the JavaScript half (OIDC, `api_server`, `audit_server` getters) is not in the repo. That a browser-hosted client needs CORS headers (`Authorization`, `Content-Type`; `GET, POST, PUT, DELETE, OPTIONS`) is an inference.
- **gzip**: legacy `GET /api/ab` sends `Accept-Encoding: gzip` explicitly. Whether the Rust `reqwest` path transparently decompresses a gzip answer to a hand-set header was not checked; not compressing responses is safe either way.
- Whether OSS hbbs/hbbr ignore the access token carried in the connection protobufs, and how OSS hbbs reacts to an `HttpProxyRequest`, was not checked (server repo not read).
- What real RustDesk Server Pro returns (exact error strings, extra fields, `licensed_devices`, `strategy.extra`) is unknown; only what the client parses is documented.
- Default values of local options `enable-flutter-http-on-rust`, `allow-ask-for-note`, `sync-ab-with-recent-sessions` were not looked up.
- `res/*.py` admin scripts and the legacy Sciter UI (apart from its API calls in `src/ui/index.tis:1541-1677`, `src/ui/ab.tis:646-672`) were not analysed.
