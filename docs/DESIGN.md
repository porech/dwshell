# dwshell — design

`dwshell` is a command-line client that opens a shell on a remote DWService
machine straight from the terminal, SSH-style, for use by humans or automated
agents. It emulates the DWService browser client (see `PROTOCOL.md`); there is
no usable public API.

## Goals

- Single, self-contained, cross-platform binary (Go).
- Interactive TTY like `ssh` (raw mode, colors, resize, full-screen apps).
- Non-interactive `-c "command"` mode for scripts/agents (capture output + exit code).
- Works with both **owned agents** and **incoming shares**.
- Correct behavior for every local-OS × remote-OS combination.
- Persist a **trusted-device token** by default so re-auth rarely needs a password.

## Non-goals (v1) — but the structure must not preclude them

Not implemented now, yet the architecture is deliberately shaped so each can be
added later as a sibling package with no core rewrite:

- **File management** over the DWService `filesystem` app, exposed scp/rsync/
  rclone-style (`get`/`put`/sync).
- **Graphical session** takeover over the `desktop` app (open a viewer window).
- A **native UI** to list machines and connect without a browser.

Also out of scope for v1: multiple simultaneous terminals in one session (the
protocol allows it; the CLI exposes one) and the legacy HTTP long-poll transport
(`simulate=true`) — we use the WebSocket.

## Architecture principle: apps over a generic session

The shell is just **one** DWService "app" running over an agent session. Login,
the command channel, machine listing, and connection are all **app-agnostic**
and reusable. Two generic session primitives carry every app:

- `session.Execute(module, command, params)` — one request/response command.
- `session.OpenSocket(module)` — a WebSocket to `?module=<app>&request=websocket`,
  returning raw frames. Each app layers its own sub-protocol on top.

`load_app(name)` + these two primitives are all any app needs, so `filesystem`
and `desktop` slot in beside `app/shell` later. **All logic lives in `internal/`
libraries; `cmd/` is only a thin CLI consumer**, so a future native UI reuses the
same login/list/connect libraries unchanged.

## Package layout

```
cmd/dwshell/        CLI: subcommand dispatch (shell default; room for cp/desktop/list)
internal/
  auth/     login (ECDH+AES-GCM, SCF), signing-key gen (ECDSA/JWK/P1363),
            trusted-device register + passwordless re-login   [app-agnostic]
  session/  command channel: Execute() + request signing (_sk / DWS-Sec-Key),
            OpenSocket() generic WebSocket, node/session bootstrap [app-agnostic]
  remote/   list owned agents + incoming shares, resolve name|id, connect,
            OS/state metadata                                  [app-agnostic]
  app/
    shell/  shell app: load_app shell + terminal JSON sub-protocol (first app)
    ...     (future) files/, desktop/ as sibling packages
  term/     local raw mode, stdin↔input, output↔stdout, SIGWINCH↔resize;
            os-specific (Unix termios / Windows console VT)    [reusable]
  config/   config file: trusted-device token, user, last node; XDG/AppData paths
docs/       PROTOCOL.md (ground truth), DESIGN.md (this file)
```

Each layer is independently testable. Crypto/auth have unit tests with vectors;
`session`/`app/shell` framing have table tests; one integration test opens a real
shell on an owned agent and runs `-c`.

## Data flow

1. **Auth** → session bootstrap (node, session id, command URL, `customHeaders`).
   - If a trusted-device token exists: passwordless `type=device` login.
   - Else: `user` then `password` step; on success register a trusted device
     (unless `--no-trusted`) and store its token.
2. **Resolve host** → list agents + shares, match by name or id (see below),
   confirm it is online (`state==N`) and supports `shell`.
3. **Connect** → `agent|share connection` with a fresh signing key → agent
   session command URL.
4. **Load app** → `core load_app name=shell`.
5. **Shell** → open WebSocket, send `init` + `open(cols,rows)`, then:
   - interactive: bridge local raw TTY ↔ `input`/`data`, `SIGWINCH` → `resize`;
   - `-c`: send the command wrapped with an exit-code sentinel, stream output,
     detect the sentinel, close, exit with that code.

## Authentication model: login is a separate action

Authenticating and using a machine are distinct. `dwshell login` is the only
command that ever asks for a password; control commands never prompt.

Two credential tiers are persisted in the config (mode 0600):

- **session** (always) — the account session: its command URL, signing key
  (private JWK), and `customHeaders`. Reused across invocations until it expires
  (DWService freezes sessions after ~20 min idle, destroys them after 24 h).
- **trustedDevice** (unless `--no-trusted`) — the permanent passwordless token
  (device id + signing key) used to refresh an expired session without a
  password. Trusted devices accumulate on the account and are **capped**, so
  `dwshell login` registers **one** and reuses it; it is never re-registered
  while a stored one exists.

Control-command resolution (`list`, `<host>`, `-c`):

1. Stored session still valid → use it.
2. Expired but a trusted device exists → silently re-login via device, refresh
   the stored session, continue.
3. Expired and no trusted device → fail with "authentication required, run
   `dwshell login`". Never prompt inline.

`--no-trusted` only means "do not register a permanent device"; the session is
still cached and reused, so re-auth is infrequent, not per-command.

## CLI

```
dwshell login [--user U] [--no-trusted]      # authenticate, persist session (+device)
dwshell logout                               # forget local creds; deregister the device
dwshell list [--json]                        # list agents + shares
dwshell [flags] <host>                       # interactive shell
dwshell [flags] <host> -c "command"          # run command, capture, exit
```

- `<host>`: agent/share **name** or **id**. If a name is ambiguous (collision
  between owned/shared, or duplicate share ids), require the id or
  `--own`/`--shared`.
- Global flags: `--term <value>`, `--no-term`, `--config <path>`, `--json`,
  `--timeout`, `-v/--verbose`.
- `login` password source (never argv): `DWSHELL_PASSWORD` env → interactive
  prompt (TTY).

## TERM / cross-OS matrix

`TERM` is a Unix concept describing the **remote** shell's terminfo. We know the
remote OS from its metadata before connecting.

| local ↓ / remote → | Linux/Mac | Windows |
|---|---|---|
| Linux/Mac | send local `$TERM` (SSH-like) | send nothing |
| Windows   | send fallback (`xterm-256color`, or `xterm` on legacy conhost) | send nothing |

- To a *nix remote, right after opening the shell send a silent
  `export TERM=<value>` (interactive) so colors/curses match the local terminal.
  Overridable via `--term`, disabled via `--no-term`.
- Never send `TERM` to a Windows remote (would print an error in cmd/PowerShell).
- Local raw mode is handled per local OS; on Windows we enable
  `ENABLE_VIRTUAL_TERMINAL_PROCESSING`/`_INPUT` and disable line input/echo
  (`golang.org/x/term` covers both platforms).

## Agent-side authentication (SSH-like)

Agents may require an OS login for the shell (`shell.enable_authentication`),
which the agent renders as an in-terminal `User:`/`Password:` prompt (see
`PROTOCOL.md` §5.5). `dwshell` drives it automatically:

- The host may be `user@host`; with no `user@`, the **local username** is used
  (like SSH).
- The username is sent automatically; the password is requested **only when the
  agent asks** — a TTY prompt interactively, or `DWSHELL_REMOTE_PASSWORD` for
  `-c` (never on argv). Wrong passwords retry (up to 3) interactively.
- Detection is by the agent's own markers: `User: ` (login needed), `Password: `,
  `Login incorrect` (retry), and the screen-clear (success). When no prompt
  appears, the initial output is treated as the session and forwarded normally.

## `-c` exit-code capture

The channel is a raw PTY, so we append a sentinel and parse it out of the stream,
choosing the form by remote shell:

- bash/sh: `<cmd>; echo __DWSH_RC_$?__`
- cmd.exe: `<cmd> & echo __DWSH_RC_%errorlevel%__`
- PowerShell: `<cmd>; echo __DWSH_RC_$LASTEXITCODE__`

Output printed up to the sentinel; the parsed number becomes `dwshell`'s exit
code. `--raw` disables sentinel wrapping (stream verbatim, exit 0).

## Config

`~/.config/dwshell/config.json` (Linux/Mac) / `%AppData%\dwshell\config.json`
(Windows), mode 0600:

```json
{"user":"<user>",
 "trustedDevice":{"id":"<id>","name":"<device>","authKey":{...private JWK...}},
 "lastNode":"<node>"}
```

The trusted-device private key is the sensitive secret; the file is user-only.

## Dependencies

- Standard library for all crypto (`crypto/ecdh`, `crypto/ecdsa` P-521/384/256,
  `crypto/aes`+`cipher` GCM, `encoding/json`, `math/big` for JWK/P1363).
- `golang.org/x/term` — raw mode + size, cross-platform.
- `github.com/gorilla/websocket` — WebSocket client.

## Testing / verification

- Unit: SCF vector; AES-GCM/ECDH round-trip; JWK⇄key and P1363⇄DER conversions;
  session-key signing format; command request/response framing; shell frame
  encode/decode.
- Integration (opt-in, needs credentials via env): log in, `list`, open a shell
  on an owned Linux agent, run `-c "echo ..."` and assert output + exit code,
  verify resize.
