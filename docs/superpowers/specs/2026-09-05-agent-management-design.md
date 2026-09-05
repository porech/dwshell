# Agent management (`dwshell agent …`) — design

Date: 2026-09-05
Status: approved for implementation pending the open questions in §9

## 1. Purpose

Create a DWService agent from the terminal and get back the installation code
that drives an unattended setup on the target machine, plus the lifecycle around
it: read the code again later, regenerate it, delete an agent, and move an agent
between groups.

Today the only way to obtain that code is the browser client. For provisioning —
the case that motivated this — that means a human clicking through a web UI in
the middle of an otherwise scripted flow.

## 2. Terminology

DWService calls these **agents**, not hosts: its own English strings shipped
with the agent (`ui/messages/default.py`) use "agent" 39 times against one
"host", which is `proxyHost`, a network proxy. The protocol modules are `agent`,
`share` and `group`. dwshell's CLI vocabulary was renamed to match before this
work started.

**Agents only, never shares.** Creating, deleting, regenerating and grouping
apply to agents you own. A share is someone else's agent; these commands refuse
one rather than failing obscurely against the service.

## 3. Protocol (reverse-engineered, to be added to PROTOCOL.md)

All of it rides the existing account command channel, which dwshell already
speaks — `session.Execute`. No new transport.

### Reading

```
module=agent command=datasource parameter operation=load
→ {"allowAdd":true,"allowDelete":true,"items":[ {…}, … ]}
```

dwshell already makes this exact call for `list`. Each item:

| field | meaning |
|---|---|
| `id` / `_id` | agent id |
| `name`, `description`, `displayName`, `fullName` | naming |
| `state` | `N` online, `F` offline, `W` **to install**, `D` disabled |
| `tempCode` | **the installation code**, non-null only while `state` is `W` |
| `idGroup`, `group` | group membership |
| `osType`, `supportedApplications`, `hwName`, `countOutgoingShares`, `dateCreation` | as today |

So reading a pending agent's code needs no new protocol at all — only a field
dwshell currently discards.

### Writing

```
module=<agent|group> command=datasource
  parameter operation=commit
  parameter changes=[{"operation":"add"|"update"|"delete","item":{…},"index":N}]
→ {"status":"ok","itemsChanged":[{"index":N,"item":{…}}]}
```

The create payload is `{idGroup, name, description}`, matching the browser
client. **The commit response returns the created item, `tempCode` included**,
so creation and code retrieval are one round trip.

Groups use the identical shape on `module=group` with `{name, description}`.

### Regenerating a code

```
module=agent command=reinstall parameter id=<agentId>
```

Puts an installed agent back into state `W` with a fresh `tempCode`.

## 4. Unattended installation

The installer parses its arguments in `fmain` (`ui/installer.py`), which accepts
`-silent`, `key=<code>`, `name=`, `group=` and `uninstall`. Only the first two
matter here: the agent already exists server-side and the code binds the
installation to it. The `user=`/`password=` path, which creates the agent during
installation, is deliberately unused — it would put account credentials on the
target machine, which is exactly what a single-use code avoids.

Download URLs, verified reachable (HTTP 200):

| target | installer |
|---|---|
| Linux, macOS | `https://www.dwservice.net/download/dwagent.sh` (1.78 MB) |
| Windows | `https://www.dwservice.net/download/dwagent.exe` (2.0 MB) — one binary; there is no `_x64` variant |

So the unattended line is, in substance:

```
# Linux / macOS, as root
curl -fsSL -o dwagent.sh https://www.dwservice.net/download/dwagent.sh && sh dwagent.sh -silent key=<tempCode>
```

Silent mode forces a real installation — it disables the installer's
"run without installing" path — so the command installs and registers a service.
`uninstall` is the reverse, which the macOS verification in §8 relies on.

`dwshell agent create` prints these lines for Linux and Windows alongside the
raw code. Their exact final form is settled by the live verification in §8:
nothing is printed that has not been run, except where §9 says otherwise.

## 5. Command surface

```
dwshell agent create <name> [--group G] [--description D] [--json]
dwshell agent code <agent> [--json]        # code of an agent still pending install
dwshell agent reinstall <agent> [--yes]    # regenerate the code
dwshell agent rm <agent> [--yes]
dwshell agent group <agent> <group>        # move into a group
dwshell agent group <agent> --none         # remove from its group
```

`--json` is available on every subcommand from the start, matching
`dwshell list --json`.

### Groups must already exist

`agent group` against an unknown group fails and lists the groups that do exist.
Creating groups is out of scope: a typo should be an error, not a new object on
the account.

### Destructive operations

`rm` and `reinstall` (which invalidates the existing code) confirm interactively,
naming the agent. With no terminal — a script, CI — they refuse unless `--yes`
is given, so automation cannot delete a machine by accident.

### `list` gains a pending state

An agent in state `W` shows today as merely offline. It becomes a distinct
`pending` state, since "created but never installed" is exactly what this feature
produces.

## 6. Structure

A new `internal/manage` package owns account mutation. `internal/remote` keeps
its present job — list, resolve a name, connect — because every command depends
on it and it should not also carry the code that deletes machines.

```
internal/manage/
  datasource.go   commit envelope (add/update/delete) shared by agent and group
  agent.go        create, delete, reinstall, set group
  group.go        resolve a group name to its id; list groups
cmd/dwshell/agent.go   the subcommand family, as files.go does for file commands
```

`remote.dsItem` grows `tempCode` and `idGroup`, and `remote.Machine` exposes the
code and the pending state, so `list` and `agent code` share one read path.

The datasource envelope is written once and serves both `agent` and `group`;
the generic "model every DWService datasource" version was rejected as
speculative generality for two consumers.

## 7. Errors

- an agent name that matches a share → refuse, naming the reason
- an unknown group → refuse, listing existing groups
- `agent code` on an agent that is not pending → say it is already installed and
  point at `agent reinstall`
- a duplicate name → the service already rejects it (`The agent {0} already
  exists`); surface its message rather than inventing one

## 8. Testing

Unit tests, against the existing fake-session pattern in `internal/`:
the commit envelope for each operation, group resolution including the
not-found path, the pending/installed distinction, and confirmation behaviour
with and without a terminal.

Live verification, since the whole point is an installation that really works:

- **Linux** — ephemeral Docker container, silent install with a real code, agent
  confirmed online, container destroyed.
- **macOS** — this workstation, installed then uninstalled.
- **Windows** — see §9.

## 9. Open questions

1. **Windows verification.** No Windows machine is available: the owned Windows
   agents are offline and the online ones are shares. A QEMU VM is possible in
   principle — the host has the aarch64 UEFI firmware and Windows 11 Arm64 ISOs
   are published, so it would be HVF-accelerated rather than emulated — but the
   workstation's disk is 98% full, with 25.7 GB free against an ISO plus an
   installation. Either free roughly 40 GB, supply a machine, or ship the
   Windows line marked as verified from the installer's source only.
2. **The commit wire format is derived from the client's code, not observed.**
   Every field is read from `mod_ui_datasource.js` and the manager packages, and
   no agent was created while establishing it. First implementation step is to
   confirm it against the live service with a throwaway agent.
