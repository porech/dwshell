# DWService protocol reference (reverse-engineered)

This document records the DWService web-client protocol as observed on the live
service (`access.dwservice.net`), by reading the client JavaScript and capturing
real network/WebSocket traffic. It is the ground truth `dwshell` re-implements.

There is **no usable public API** for this: the documented REST API
(`apiremoteaccess.com`, `docs.dwservice.net/docs/api`) is a separate whitelabel/
reseller product that requires its own API key/secret and only returns an iframe
URL — it cannot drive a shell. So `dwshell` emulates the browser client.

Throughout, identifiers are shown as placeholders (`<node>`, `<sessionId>`,
`<token>`, `<agentId>`, etc.). Any literal-looking value is illustrative.

---

## 0. Hosts and terminology

- `access.dwservice.net` — front-end + authentication.
- `res-access.dwservice.net` — static resources (CORS mirror; `https://` →
  `https://res-...`).
- After login the account is pinned to a **node**, e.g. `<node>.dwservice.net`.
- WebSocket relay host prefixes the node: `wss://s<N>-<node>.dwservice.net`
  (the `s<N>-` prefix and `slot=<N>` are assigned per socket).
- **Session id** e.g. `ND######`. **Session token**: an opaque ~40-char string
  embedded in the command URL path.
- **Agent**: a machine you own. **Share**: a machine another account shared with you.

---

## 1. Cryptography primitives

### 1.1 Login payload encryption (ECDH + AES-GCM)

Config (`dwsConfig.cryptAlgorithmAccept[0]`), read live from the login page:

- Server public key (SPKI, base64), curve **P-256** (rotates; must be read live).
- Client generates an ephemeral **ECDH P-256** key pair.
- Shared secret derived via ECDH; used as an **AES-GCM 256** key.
- IV: 16 random bytes per message.

Encrypt procedure (`dwsInitEncrypt`):

1. `plaintext = JSON.stringify(payload)` (UTF-8).
2. Import server SPKI public key (P-256).
3. Generate ephemeral P-256 key pair; export client public key as SPKI.
4. `aesKey = ECDH(clientPriv, serverPub)` → AES-GCM 256.
5. `iv = random(16)`.
6. `ciphertext = AES-GCM(aesKey, iv, plaintext)` (tag appended; WebCrypto default 128-bit tag).
7. Emit token object:
   ```json
   {"encrypt": true,
    "value": "<base64(ciphertext||tag)>",
    "publicKey": "<base64(client SPKI)>",
    "iv": [16 ints]}
   ```

The token object is then `JSON.stringify`-ed and sent as the `token` form field.

### 1.2 Anti-tamper field (`getSCF`)

Alongside `token`, the client sends a derived checksum field. Given the token
JSON string `t`, with `l = t.length`, field name `AdgJklfeT1rtA`, value =
concatenation of `t[i % l]` for this fixed index list:

```
23,15,71,41,21,2,12,35,86,17,8,18,13,9,26,9,24,6,11,31
```

(20 characters). The server rejects requests where this does not match.

### 1.3 Session signing key (ECDSA, per session)

At login (password step) and at every agent/share connection, the client
generates a **signing** key pair used to authenticate subsequent requests of
that session. Algorithm preference order (`dwsInitSessionGenKey`):

```
SIGN_ECDSA_512 (P-521, SHA-512)  ← used in practice
SIGN_ECDSA_384 (P-384, SHA-384)
SIGN_ECDSA_256 (P-256, SHA-256)
SIGN_HMAC_512 / 384 / 256
```

Key material is exchanged as **JWK**. The `sessionKey` object sent to the server:

```json
{"generate": true,
 "name": "SIGN_ECDSA_512",
 "verify": {"key": { <public JWK: crv,kty,x,y,ext,key_ops:["verify"]> }},
 "initValue": "<base64( ascii(N) ':' rawSig )>"}
```

- `N` = a random negative integer (ascii digits, e.g. `-4091356`).
- `rawSig` = ECDSA signature of `ascii(N)` in **IEEE P1363 raw r‖s** form
  (this is what WebCrypto produces — NOT ASN.1 DER). For P-521 that is 132 bytes.
- The private JWK is kept locally (never sent).

**Request signing** derives a per-request key string:

- **Session key** (`getNewSessionKey`, used for command POSTs and the WebSocket):
  counter = current epoch millis, strictly increasing per session; value =
  `base64( ascii(counter) ':' rawSig(ascii(counter)) )`.
- **Reconnect key** (`getNewReconnectKey`, used for `?request=initialize`):
  counter starts at `N` (from `initValue`) and **decrements** by 1 each use;
  same encoding.

The signed key is delivered either as the `DWS-Sec-Key` HTTP header (when the
node advertises `customHeaders: true`) or as a `&_sk=<urlencoded key>` query
parameter (fallback).

> Empirical note: the very first `?resptype=json` bootstrap request was observed
> using `_sk=base64("<N>:")` with an **empty** signature. `dwshell` follows the
> JS exactly (sign the counter) and reconciles against the live server during
> integration testing; the opaque session token in the URL path is itself a
> bearer credential.

---

## 2. Login (`POST https://access.dwservice.net/authentication.dw`)

`Content-Type: application/x-www-form-urlencoded`. Two steps.

### Step `user`

Request form fields: `type=login`, `step=user`, `token=<encrypted {username}>`,
`AdgJklfeT1rtA=<scf>`.

Encrypted payload: `{"type":"login","step":"user","username":"<user>"}`.

Response:
```json
{"tempKey":"<tempKey>","userName":"<user>","status":"password"}
```

Other possible `status`: `totp` / `email` / `device` (2FA), `captcha`.

### Two-factor step (`totp` / `email`)

When 2FA is enabled, the **password** step returns `status: "totp"`, `"email"`,
or `"device"` (plus a fresh `tempKey`) instead of `"ok"`. The client then submits
a step named after the method:

- **TOTP:** `step="totp"`, `password="<tempKey>:<6-digit code>"`.
- **Email:** first `step="email"`, `password="<tempKey>:EMAIL"` to make the server
  send the code (the response carries a new `tempKey`); then `step="email"`,
  `password="<tempKey>:<code>"`.
- **Device:** approve-on-device — the client polls with `password="<tempKey>"`
  (no code) every ~2s while the status stays `device`/`wait`, until the user
  approves on a trusted device and it becomes `ok`.

The 2FA step also carries a fresh `sessionKey` (and `trustedDevice` if
registering); the key from the step that finally returns `status:"ok"` becomes
the session's signing key. A wrong/expired code comes back as the same 2FA
`status` (with a message) and a refreshed `tempKey`, so the client can retry.
A registered trusted device logs in passwordlessly (§6) and skips 2FA entirely.

### Step `password`

Encrypted payload:
```json
{"type":"login","step":"password",
 "username":"<user>",
 "password":"<tempKey>:<plaintext password>",
 "sessionKey": { ...public signing key (§1.3)... },
 "trustedDevice": { ...optional, see §6... }}
```

Responses:
- Success `status:"ok"` — carries the session bootstrap. In the browser this is
  handed to `session.dw` via sessionStorage; the node then serves the config:
  ```json
  {"baseUrl":"https://<node>.dwservice.net/",
   "commandUrl":"https://<node>.dwservice.net/ses/<sessionId>/<token>.dw",
   "mainPage":"ManagerMain","userName":"<user>","userType":"BASIC",
   "customHeaders":true, "language":"<lang>", ...}
  ```
- `status:"error"`, `message:"#PASSWORDRESET"` — password expired → must reset.
- `#PASSWORDWEAK`, `#INVALIDTOKEN`, `#InvalidAuthentication`, etc.

### Password reset (`type=reset`)

Steps: `email` (+ ALTCHA captcha; returns `tempKey`, `status:"password"`) →
`password` (new password twice; returns `status:"code"` and emails a code) →
`code` (returns `status:"ok"`). Payloads encrypted as in §1.1.

---

## 3. Account command channel

Endpoint: `POST <commandUrl>?request=command` (also `?request=initialize`,
`?request=keepalive`). `commandUrl` from the bootstrap. Auth per §1.3.

### Request body (form-urlencoded)

```
count=<N>
id_0=K1&module_0=<module>&command_0=<command>&parameter_0_<k>=<v>...
id_1=K2&module_1=...&...
```

### Response framing

First char is the status: `K` ok, `E` error, `D` disconnect/expired,
`B` retry another node, `W` wait-accept, `P` password-request.

For `K`, the body is a concatenation, per command:
`<cmdId>:<len>:<K|E|...>:<payload>` where `<payload>` is `len` bytes. For a
command that returns JSON the payload after the inner `K:` is the JSON text.
Empty payload (`K:`) = success with no data.

Example:
```
POST ...?request=command
  &count=1&id_0=K1&module_0=user&command_0=listNotifications
→ K:K1:<len>:K:[{...notifications json...}]
```

### `initialize`

`GET <node>/ses/<sessionId>/<token>.dw?request=initialize` (reconnect key).
Response `K:<json>` with `keepAliveInterval`, `customHeaders`, quotas,
`globalVars`. `D:` = expired.

### Keepalive

`POST <commandUrl>?request=keepalive` every `keepAliveInterval` (config default
~20s) when idle. Response first char `K`/`P`/`W`/`D`.

---

## 4. Listing machines

### Owned agents

```
module=agent command=datasource parameter operation=load
→ {"items":[
     {"name":"<name>","displayName":"<name>","id":"<agentId>","_id":"<agentId>",
      "osType":0,"state":"N",
      "supportedApplications":"filesystem;texteditor;logwatch;resource;desktop;shell",
      "group":null, ...}, ...],
   "status":"ok"}
```

- `osType`: **0 = Linux, 1 = Windows, 2 = Mac**.
- `state`: `N` available/online, `F` unavailable/offline, `W` to-install, `D` disabled.
- `supportedApplications`: `;`-separated; must contain `shell`.

### Incoming shares

```
module=share command=datasource parameter operation=load parameter name=incoming
→ {"items":[
     {"name":"<name>","agentOsType":1,"state":"N",
      "_id":"<shareId>","idAgent":"<idAgent>",
      "userDisplayName":"<owner>",
      "permissions":{"fullAccess":true,"applications":[]},
      "group":"<group>", ...}, ...],
   "status":"ok"}
```

- `agentOsType` same mapping as `osType`.
- Share `_id` values may be **non-unique**; the unique key is `idAgent`.
- `permissions.applications` empty + `fullAccess:true` = all apps allowed;
  otherwise it lists allowed app names.

(`name=outgoing` lists shares you granted — not connectable by you.)

---

## 5. Opening a machine session and the shell

### 5.1 Connect

Owned agent:
```
module=agent command=connection
  parameter agent=<agentId>
  parameter sessionKey=<JSON new signing key, §1.3>
  parameter newresp=true
→ {"url":"https://<node>.dwservice.net/ses/<sessionId>/<agentToken>.dw",
   "status":"ok"}
```

Share:
```
module=share command=connection
  parameter share=<shareId>@<idAgent>
  parameter sessionKey=<JSON new signing key>
  parameter newresp=true
→ {"url":"...","status":"ok"}
```

The returned `url` is the **agent session's command URL** (same node/session id,
new token). A *new* signing key pair is generated for this agent session; sign
its requests with it.

### 5.2 Load the shell app

On the agent session's command channel:
```
module=core command=load_app parameter name=shell
→ K:            (empty success)
```

### 5.3 Shell WebSocket

```
wss://s<N>-<node>.dwservice.net/ses/<sessionId>/<agentToken>.dw
    ?module=shell&request=websocket&simulate=false&slot=<N>&_sk=<sessionKey>
```

- `s<N>-` prefix and `slot` are assigned by the client/relay (start at `slot=1`;
  `simulate=false` selects a true WebSocket; `true` would select HTTP long-poll).
- `_sk` is the session signing key (§1.3), fresh per connect.
- Where `customHeaders` is supported the client may instead send `DWS-Sec-Key`;
  for the WebSocket the `_sk` query parameter was used.

A legacy binary framing exists (`_TMPnewConnMode==0`, length-prefixed frames,
type byte `s`=string). The modern mode delivers **plain JSON text frames**, one
message per frame.

**One message must be one unfragmented frame.** The relay node closes the
connection (`1005`, nothing reaching the agent) the moment a message arrives
split across WebSocket continuation frames. The browser client never fragments —
its socket layer hands each JSON message to `WebSocket.send()` whole — so this
is easy to miss with a Go client, where `gorilla/websocket` fragments any
message larger than its write buffer (4096 bytes by default). Verified on Linux
and Windows agents: the failure threshold tracks the write buffer exactly.

Message size has two further ceilings, both agent-side rather than protocol:
a single message carrying much more than ~10 KB is unreliable (the browser
client drops the connection there too), and pushing more than roughly 20 KB of
input in an unpaced burst tears the terminal down — the agent writes input to
the PTY while holding the lock its reader thread needs, so a full PTY buffer
blocks the write, starves the reader, and trips its timeout.

### 5.4 Shell sub-protocol (JSON text frames)

Client → server:

| message | meaning |
|---|---|
| `{"type":"init"}` | announce (sent once on connect) |
| `{"id":<n>,"type":0,"cols":<c>,"rows":<r>}` | **open** terminal `n` with size |
| `{"id":<n>,"type":2,"data":"<bytes>"}` | **input** (keystrokes, raw) |
| `{"id":<n>,"type":3,"rows":<r>,"cols":<c>}` | **resize** |
| `{"id":<n>,"type":1}` | **close** terminal `n` |
| `{"type":"keepalive"}` | every 30s |
| `{"type":"term"}` | terminate the whole shell app |

Server → client:

| message | meaning |
|---|---|
| `{"type":"info","version":1,"ids":[...]}` | list of live terminal ids |
| `{"type":"data","id":<n>,"data":"<bytes>"}` | terminal output (raw, incl. ANSI) |
| `{"id":<n>,"terminate":true}` | terminal `n` ended |

`id` is a small client-assigned integer (first terminal = 1). `data` is raw
terminal bytes as a JSON string (the PTY echoes input back). Enter = `\r`.

Captured Linux open (a real PTY; prompt with bracketed-paste + OSC title):
```
→ {"id":1,"type":0,"cols":139,"rows":44}
→ {"type":"init"}
← {"type":"info","version":1,"ids":[1]}
← {"type":"data","id":1,"data":"\u001b[?2004h\u001b]0;user@host: ~\u0007user@host:~# "}
```

Captured Windows open (cmd.exe over ConPTY; note the VT sequences):
```
← {"type":"data","id":1,"data":"\u001b[2J\u001b[m\u001b[HMicrosoft Windows [Version ...]\u001b]0;C:\\WINDOWS\\SYSTEM32\\cmd.exe\u0007\u001b[?25h\r\n(c) Microsoft Corporation...\r\nC:\\>..."}
```

---

## 5.5 Agent-side shell authentication (in-terminal login)

An agent can require OS-level authentication for the shell app via its config
(`/usr/share/dwagent/config.json`, flat keys):

- `"shell.enable_authentication": true` — require a username+password to open a
  shell (only enforced when the agent runs as root).
- `"shell.users_allowed": [{"name":"root","enable":true},{"name":"*","enable":false}]`
  — restrict which OS users may log in (`*` wildcard).

This is **not** a separate protocol message: it is an interactive login prompt
rendered over the normal terminal data channel (verified against the agent's
`app_shell/shell.py`). On terminal open the agent:

1. sends `\x1b[2J\x1b[HUser: ` (clear screen + prompt);
2. reads the username as terminal input (echoed) until `\r`;
3. sends `\r\nPassword: ` and reads the password (echoed as `*`) until `\r`;
4. validates it (`crypt` against the OS user) and `is_user_allowed`;
5. on success sends `\x1b[2J\x1b[H` and starts the PTY **as that OS user**;
   on failure sends `\r\nLogin incorrect`, waits, and repeats from step 1.

A client provides SSH-like UX by auto-sending the username and prompting for the
password only when `User: ` appears, detecting `Login incorrect` for retries and
the screen-clear for success.

There is also a separate, session-level access password (agent global): a command
returns status `P` (password required) / `W` (wait for the agent user to accept),
and the client submits it via `POST <commandUrl>?request=checkpassword` with body
`{"password": "..."}` (response `K` ok / `W` wait-accept / error). dwshell focuses
on the per-shell in-terminal auth above.

## 6. Trusted device (permanent token)

When `trustedDevice` is included in the password-step payload, the client
generates a **second** signing key pair (`authKey`) and registers it:

```json
"trustedDevice": {
  "name":"<device name>", "type":"<Desktop|...>", "os":"<os>",
  "authKey": { "name":"SIGN_ECDSA_512",
               "verify":{"key":<public JWK>},
               "initValue":"<base64(ascii(N) ':' rawSig)>" }
}
```

On success the response carries `trustedDeviceID` and `trustedDeviceUserName`.
The client stores `{id, name, authKey(private JWK)}`.

**Passwordless re-login** (`type=device`):
```
type=device step=user token=<encrypted {id, auth}>
  where auth = sign(ascii(now), authKeyPriv)   # base64(ascii(now) ':' rawSig)
```
Returns the same session bootstrap as a normal login. This is the permanent
token `dwshell` persists in its config (default), avoiding password re-entry.

---

## 7. TERM / colors / resize (verified)

- The remote is a **real PTY/ConPTY** on all OSes — colors, cursor control,
  resize, and full-screen apps (nano, vim, htop) work.
- The protocol has **no TERM field**; the remote decides `TERM`. On Linux the
  agent set `TERM=xterm` (`tput colors` = 8). To get 256 colors, `dwshell`
  sends `export TERM=<value>` right after opening a *nix shell.
- **Never** send a `TERM` export to a Windows remote (cmd/PowerShell) — it is not
  a shell builtin and would print an error.
- Resize is a real SIGWINCH via `type:3`; verified (`tput cols`×`lines` matched
  the values sent).

---

## 8. Filesystem app

The `filesystem` app runs over the same agent session as the shell: first
`core/load_app name=filesystem` (the agent may lazily download the app on first
use — retry once), then:

### Metadata / operations (command channel)

`POST <commandUrl>?request=command` with `module=filesystem`:

- `list` — `parameter path=<dir>` (optional `filterList=<JSON array of names>`,
  `filterIgnoreCase=true`). Response:
  ```json
  {"items":[
     {"Name":"D:bin","LastModified":1787510458498,"Rights":"755","Owner":"root","Group":"root"},
     {"Name":"F:hosts","LastModified":1688578959000,"Length":221,"Rights":"644","Owner":"root","Group":"root"}
   ],
   "permissions":{"apps":{"texteditor":{},"logwatch":{}}}}
  ```
  `Name` is prefixed `D:` (directory) or `F:` (file); `Length` (bytes) is present
  for files; times are epoch ms; `Rights` is octal.
- `makedir` — `path`, `name`.
- `rename` — `path`, `name`, `newname`.
- `remove` — `path`, `files` (JSON array of names). Response
  `{"items":[{"Name":"<K|E>:<name>"}]}` — `K:` removed, `E:` failed.
- `set_permissions` — `path`, `name`, `mode`, `owner`, `group`, `recursive`.

### Transfers (HTTP, `_sk` query auth)

Transfers authenticate with the `_sk` query parameter (not the DWS-Sec-Key
header), and `key` is a client-generated id (no handshake):

- **Download** — `GET <commandUrl>?module=filesystem&request=download&path=<urlenc(fullPath)>&key=K<n>&_sk=<sessionKey>` streams the raw file bytes.
- **Upload** — `POST` the same URL with `request=upload` and a
  `multipart/form-data` body whose `UPFile` field is the file content. Send a
  `Content-Length` (buffer the body); the node's upstream rejects chunked uploads.

The filesystem app has **no checksum** in its metadata and **no set-mtime**
command; dwshell's sync plans work around both using the shell (see DESIGN.md).
