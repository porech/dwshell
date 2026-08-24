# dwshell

A command-line client that opens a shell on a remote [DWService](https://www.dwservice.net)
machine straight from your terminal, SSH-style — for humans or automated agents.

DWService's browser client offers a "Shell" application; `dwshell` speaks the
same protocol from the terminal, so you get a real remote shell (with colors,
resize, and full-screen apps like `nano`/`vim`/`htop`) without a browser. It
works with machines you own (**agents**) and machines shared with you
(**shares**), on Linux, Windows, and macOS remotes.

There is no usable public API for this — the documented REST API is a separate
whitelabel product that only returns an iframe URL — so `dwshell` emulates the
browser client. The protocol is documented in [`docs/PROTOCOL.md`](docs/PROTOCOL.md);
the design in [`docs/DESIGN.md`](docs/DESIGN.md).

## Why?

DWService is, by far, my favourite remote-control service, and I think it should
be everyone's. These are some of the things I love the most about it:

- **The control client runs entirely in the browser.** You only install the
  agent on the machines you want to reach — nothing on the machine you connect
  from.
- **First-class Linux support, even on headless machines:** the install script
  works cleanly from the terminal.
- **You don't have to run a graphical session if you don't need one.** You can
  just transfer files or spawn a shell — a lifesaver when the agent side has
  little bandwidth and you only need to copy a small file or run a few commands.
- **Affordable, clear plans** with no obscure machinery to separate personal from
  commercial use.
- **A very generous free plan** — usually all you'll ever need.

Having the control side entirely in the browser is normally a plus, but with the
shell feature I sometimes missed being able to launch it without leaving my
terminal, the way I would with SSH. That's what this tool is for.

## Install

`dwshell` is a single self-contained binary (pure Go, no cgo). Pick one:

### Download a pre-built binary (recommended)

Get the archive for your OS/arch from the **[latest release](https://github.com/porech/dwshell/releases/latest)**
(Linux/macOS/Windows, amd64/arm64), extract it, and put the `dwshell` binary on
your `PATH`. For example, on Linux/macOS:

```sh
# replace the URL with the asset matching your OS/arch from the release page
curl -sSL -o dwshell.tar.gz \
  https://github.com/porech/dwshell/releases/latest/download/dwshell_<version>_<os>_<arch>.tar.gz
tar -xzf dwshell.tar.gz dwshell
sudo mv dwshell /usr/local/bin/
```

On Windows, download the `.zip`, extract `dwshell.exe`, and place it in a folder
on your `PATH`.

### With `go install`

Requires Go 1.25+:

```sh
go install github.com/porech/dwshell/cmd/dwshell@latest
```

This installs `dwshell` into `$GOBIN` (or `$(go env GOPATH)/bin`).

### Build from source

```sh
git clone https://github.com/porech/dwshell.git
cd dwshell
go build -o dwshell ./cmd/dwshell
```

### Verifying

```sh
dwshell --version      # e.g. "dwshell v1.0.0 (abc123def456) 2026-…"
```

The version is baked in at release time; `go install` reports the module version,
and source builds report the commit (plus a `-dirty` marker when the working tree
has uncommitted changes).

## Quick start

```sh
# 1. Authenticate once with your DWService account (email + password).
#    This is the only command that ever asks for a password.
$ dwshell login
User (email): you@example.com
Password: ********
logged in; session saved (trusted device registered) to ~/.config/dwshell/config.json

# 2. See which machines you can reach (owned agents + shares shared with you).
$ dwshell list
myserver        Linux    online  own     aBcD...
buildbox        Windows  online  shared  wXyZ...

# 3. Open an interactive shell (SSH-style).
$ dwshell myserver
root@myserver:~#

# 4. Or run a single command and capture its output / exit code.
$ dwshell myserver -c "uname -sr"
Linux 6.8.0
```

After `login`, control commands (`list`, `<host>`, `-c`) reuse the saved session
and **never prompt** for the account password. When the session expires they
refresh silently via the trusted device, or, if there is none, tell you to run
`dwshell login` again. Leave an interactive shell with `exit` (or Ctrl-D).

If your account has **two-factor authentication**, `login` handles it: it prompts
for the code (TOTP, or the code emailed to you), or waits while you approve the
sign-in on a trusted device (device 2FA). A registered trusted device refreshes
the session without any of this. To supply a code non-interactively, see
[Environment variables](#environment-variables).

### Commands

| Command | Description |
|---|---|
| `dwshell login [--user U] [--no-trusted]` | Authenticate and persist the session (and, by default, a trusted device). |
| `dwshell logout` | Deregister the trusted device and forget local credentials. |
| `dwshell list [--json]` | List machines with OS, online state, and owned/shared. |
| `dwshell <host>` | Open an interactive shell. |
| `dwshell <host> -c "cmd"` | Run a command non-interactively; exit code is propagated. |
| `dwshell shell <host>` | Explicit form of the above. |
| `dwshell ls <host>:<path>` | List a remote directory. |
| `dwshell get [-r] <host>:<remote> [local]` | Download a file (or directory with `-r`). |
| `dwshell put [-r] <local> <host>:<remote>` | Upload a file (or directory with `-r`). |
| `dwshell rm [-r] <host>:<path> [...]` | Remove remote file(s) (directories with `-r`). |
| `dwshell sync [-n] <src> <dst>` | One-way sync (size+mtime); one side is `host:path`. |
| `dwshell version` | Print the version and exit. |
| `dwshell help` | Show usage. |

#### File transfer

`ls`, `get`, `put`, and `rm` operate on the DWService filesystem app. Remote endpoints
are written `host:path` (the split is on the first colon, so a remote Windows
path like `GHE:C:\Users` works):

```sh
dwshell ls GHE:/etc                     # list a remote directory
dwshell get GHE:/etc/os-release         # download into ./os-release
dwshell get GHE:/var/log/syslog -       # download to stdout
dwshell put ./report.pdf GHE:/tmp/      # upload (trailing / keeps the name)
cat data | dwshell put - GHE:/tmp/data  # upload from stdin
dwshell rm GHE:/tmp/old.log GHE:/tmp/x  # remove one or more files
dwshell put -r ./site GHE:/var/www/site # upload a directory tree
dwshell get -r GHE:/etc/nginx ./nginx   # download a directory tree
dwshell rm -r GHE:/tmp/build            # remove a directory tree
dwshell sync ./site GHE:/var/www/site   # upload-sync (only changed files)
dwshell sync -n GHE:/data ./data        # dry-run download-sync
```

Single-file transfers, recursive `get -r` / `put -r` / `rm -r`, and one-way
`sync` (transfers only files that differ by size or mtime). `sync` takes exactly
one `host:path` side; direction is inferred. It preserves mtimes (locally on
download; on upload it sets the remote mtime via the shell — falling back to
size-only when that is unavailable, e.g. on Windows remotes). `--size-only`
compares by size only; `-n` is a dry run.
`--own` / `--shared` disambiguate the host as elsewhere. On Windows remotes `/`
and `\` are interchangeable.

#### Host name vs subcommand

`dwshell <host>` is a convenience shortcut: the first argument is treated as a
host **unless** it exactly matches one of the subcommands in the
[Commands](#commands) table above. So `dwshell version` prints the version, it
does not connect to a machine.

If you actually have a machine named like one of those, use the explicit `shell`
subcommand, which always treats its argument as a host:

```sh
dwshell shell version      # connect to the host named "version"
dwshell shell list -c "id" # run a command on the host named "list"
```

`<host>` is a machine **name** or **id**, optionally prefixed `user@` (SSH-style;
defaults to your local username). If a name is ambiguous (a name shared between an
owned agent and a share, or duplicate share names), pass the id or add `--own` /
`--shared`. The `user@` part matters only when the agent requires authentication
(below); when it does not, the shell runs as the agent's own user and an
explicitly given `user@` is ignored with a note.

### Agent-side authentication

Some agents require an OS login to open the shell (`shell.enable_authentication`).
`dwshell` handles it SSH-style: it sends the username automatically (from `user@`
or your local username) and asks for the password **only when the agent requires
it**. For `-c`, provide the remote password via `DWSHELL_REMOTE_PASSWORD` (it is
never taken from the command line). Access-restricted users
(`shell.users_allowed`) are enforced by the agent.

```sh
dwshell alice@myhost              # log in as alice; prompts for her password if required
DWSHELL_REMOTE_PASSWORD=… dwshell alice@myhost -c "id"
```

### Flags

- `-c <command>` — run a command non-interactively and exit with its code.
- `--own` / `--shared` — resolve `<host>` among owned agents / incoming shares only.
- `--term <value>` — TERM to send to a *nix remote (default: your local `$TERM`).
- `--no-term` — do not send a TERM to the remote.
- `--timeout <dur>` — command timeout for `-c` (default: none; e.g. `30s`, `5m`).
- `--config <path>` — config file location.
- `--json` — machine-readable `list` output.

### Environment variables

- `DWSHELL_PASSWORD` — account password for `login` (avoids the prompt).
- `DWSHELL_TOTP_CODE` — a ready TOTP code (only TOTP; an emailed code can't be
  known in advance, so provide it at the prompt or on stdin).
- `DWSHELL_REMOTE_PASSWORD` — remote OS password when the agent requires shell
  authentication (used by `-c`, and by interactive before the first prompt).
- `DWSHELL_CONFIG` — override the config file path.

Passwords are never read from command-line arguments.

**Advanced:** `DWSHELL_TOTP_SECRET` — the TOTP shared secret; when set, `login`
generates the code itself, enabling fully unattended login. Storing the secret
alongside the password largely defeats the point of a second factor, so use this
only for automation where you accept that trade-off.

## Authentication

Two credentials are stored (config file, mode 0600):

- the **account session** — reused across invocations until it expires
  (DWService freezes idle sessions after ~20 min, destroys them after 24 h);
- a **trusted device** token (unless `--no-trusted`) — a permanent passwordless
  credential used to refresh an expired session without a password.

Trusted-device registrations are capped per account, so `dwshell` registers at
most one and reuses it; `dwshell logout` deregisters it to free the slot.

The config file lives at `~/.config/dwshell/config.json` (or `$XDG_CONFIG_HOME`)
on Linux/macOS and `%AppData%\dwshell\config.json` on Windows, overridable with
`--config` or `DWSHELL_CONFIG`. It is created mode 0600 and holds the session
signing key and trusted-device token — treat it as a secret.

## Terminal behavior

The remote is a real PTY (ConPTY on Windows), so colors, cursor control, resize
(`SIGWINCH`), and full-screen apps work. `TERM` is a *nix concept, so `dwshell`
propagates your local `$TERM` to Unix remotes (falling back to `xterm-256color`
when unset, e.g. on Windows) and sends nothing to Windows remotes. Override with
`--term`, disable with `--no-term`. If a remote lacks the terminfo entry for your
`$TERM` (curses apps complain), pass `--term xterm-256color`.

## Scope

v1 focuses on the shell. The code is structured so other DWService apps can be
added as sibling packages without reworking the core: file transfer (the
`filesystem` app), graphical session takeover (the `desktop` app), and a possible
native UI all reuse the same login/session/connect libraries under `internal/`.

## Status

Reverse-engineered and verified end-to-end against the live service on Linux and
Windows remotes. Unit tests cover the crypto, request framing, and host
resolution; `go test ./...`.

## Legal & disclaimer

`dwshell` is an **independent, unofficial** client. It is **not affiliated with,
authorized, sponsored, or endorsed by** DWService or its owner. "DWService" and
any related names or logos are trademarks of their respective owner and are used
here only nominatively, to describe compatibility.

- **No DWService code is included.** This project is a clean-room reimplementation
  based on observable protocol facts (endpoints, message formats, field names).
  DWService's browser client ("the Application") is proprietary and is **not**
  copied, bundled, or redistributed here. `docs/PROTOCOL.md` documents the wire
  protocol for interoperability; it contains no DWService source code.
- **Interoperability.** The project exists to interoperate with a service the
  user already has an account on. In the EU, creating an independent, compatible
  program is supported by the Software Directive (2009/24/EC, art. 6).
- **Your responsibility.** Use `dwshell` only with your own DWService account and
  only to reach machines you are authorized to access. You remain bound by
  DWService's Terms & Conditions; using a non-official client is your decision and
  risk. DWService's terms permit personal use of the service but restrict copying/
  redistributing *their* Application — which this project does not do.
- **License.** `dwshell`'s own source is released under the [MIT License](LICENSE).

This section is informational, not legal advice. If you need certainty for your
situation, consult a lawyer or request written authorization/clarification from
DWService.

> Note for contributors: never commit DWService's client/agent source or assets.
> A local `/.reverse-engineering/` scratch directory (if you recreate one) is
> git-ignored for this reason.
