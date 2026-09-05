# Agent management Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create a DWService agent from the terminal, get the installation code that drives an unattended setup, and manage the lifecycle around it (read the code, regenerate it, delete, move between groups).

**Architecture:** A new `internal/manage` package owns account mutation over the existing account command channel; `internal/remote` keeps its present job (list, resolve, connect) and only grows the fields it already receives. The CLI family lives in `cmd/dwshell/agent.go`, as file commands live in `files.go`.

**Tech Stack:** Go, standard library only. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-09-05-agent-management-design.md`

## Global Constraints

- **Agents only, never shares.** Every subcommand refuses a share, naming the reason.
- **Never print a download-and-run one-liner.** The DWService download page carries the licence acceptance ("By selecting the 'Download' button I accept the Terms and Conditions…"). Print the page `https://www.dwservice.net/download.html` and a run line that assumes the installer is already there. The direct file URLs are known and deliberately unused.
- **`tempCode` is a JSON number, not a string** — verified live: `"tempCode":281407902`. Decode it as an integer.
- **Never print or pass the code undashed.** The client renders it in groups of three (`S.substring(0,3)+"-"+S.substring(3,6)+"-"+S.substring(6)` → `281-407-902`) and the installer forwards the code with only whitespace stripped, keeping the dashes, so the dashed form is what the service expects. Pad to nine digits before grouping: a leading zero cannot survive a JSON number.
- **`osType` is `null` until an agent is installed**, so a pending agent has no meaningful OS.
- `--json` on every subcommand, matching `dwshell list --json`.
- Destructive operations (`rm`, `reinstall`) confirm interactively naming the agent; with no terminal they refuse unless `--yes`.
- Groups must already exist; an unknown group is an error listing the ones that do.

## Verified protocol

Confirmed against the live service on 2026-09-05 (an agent was created and deleted):

```
create  module=agent command=datasource
        operation=commit
        changes=[{"operation":"add","index":0,"item":{"idGroup":null,"name":"N","description":"D"}}]
     → {"itemsChanged":[{"item":{…,"id":"…","state":"W","tempCode":281407902},"index":0}],"status":"ok"}

delete  changes=[{"operation":"delete","index":0,"item":{"_id":"ID","id":"ID"}}]
     → {"status":"ok"}

load    operation=load                    (already used by `list`)
     → {"allowAdd":true,"allowDelete":true,"items":[…]}
```

Derived from the client but **not yet exercised** — each is confirmed live by the task that implements it:

```
update  changes=[{"operation":"update","index":0,"item":{…,"idGroup":"GID"}}]
group   module=group command=datasource operation=load
reinst. module=agent command=reinstall parameter id=<agentId>
```

## File Structure

| File | Responsibility |
|---|---|
| `internal/manage/datasource.go` (new) | the commit envelope: add/update/delete + response decoding, shared by agent and group |
| `internal/manage/agent.go` (new) | create, delete, reinstall, set group |
| `internal/manage/group.go` (new) | list groups, resolve a group name to its id |
| `internal/remote/remote.go` (modify) | decode `tempCode`/`idGroup`; `Machine.InstallCode`, `Machine.Pending` |
| `cmd/dwshell/agent.go` (new) | the `agent` subcommand family and its output |
| `cmd/dwshell/main.go` (modify) | dispatch `agent`; help text |
| `README.md`, `docs/PROTOCOL.md` (modify) | document the commands and the protocol |

---

### Task 1: Datasource commit envelope

**Files:**
- Create: `internal/manage/datasource.go`
- Test: `internal/manage/datasource_test.go`

**Interfaces:**
- Consumes: `session.Session.Execute(ctx, module, command string, params map[string]string) ([]byte, error)`
- Produces:
  ```go
  type Executor interface {
      Execute(ctx context.Context, module, command string, params map[string]string) ([]byte, error)
  }
  type item map[string]any
  func commit(ctx context.Context, ex Executor, module string, changes []change) ([]item, error)
  type change struct {
      Operation string `json:"operation"` // "add" | "update" | "delete"
      Index     int    `json:"index"`
      Item      item   `json:"item"`
  }
  ```

- [ ] **Step 1: Write the failing test**

```go
package manage

import (
	"context"
	"encoding/json"
	"testing"
)

type fakeExec struct {
	gotModule, gotCommand string
	gotParams             map[string]string
	resp                  string
}

func (f *fakeExec) Execute(_ context.Context, module, command string, p map[string]string) ([]byte, error) {
	f.gotModule, f.gotCommand, f.gotParams = module, command, p
	return []byte(f.resp), nil
}

func TestCommitSendsTheChangesEnvelope(t *testing.T) {
	f := &fakeExec{resp: `{"status":"ok","itemsChanged":[{"index":0,"item":{"id":"A1","tempCode":281407902}}]}`}
	items, err := commit(context.Background(), f, "agent",
		[]change{{Operation: "add", Index: 0, Item: item{"name": "n1"}}})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if f.gotModule != "agent" || f.gotCommand != "datasource" {
		t.Fatalf("sent to %s/%s", f.gotModule, f.gotCommand)
	}
	if f.gotParams["operation"] != "commit" {
		t.Fatalf("operation = %q", f.gotParams["operation"])
	}
	var sent []change
	if err := json.Unmarshal([]byte(f.gotParams["changes"]), &sent); err != nil {
		t.Fatalf("changes is not JSON: %v", err)
	}
	if len(sent) != 1 || sent[0].Operation != "add" || sent[0].Item["name"] != "n1" {
		t.Fatalf("changes = %+v", sent)
	}
	if len(items) != 1 || items[0]["id"] != "A1" {
		t.Fatalf("items = %+v", items)
	}
}

// The service reports failure in the body, with status 200.
func TestCommitSurfacesTheServiceMessage(t *testing.T) {
	f := &fakeExec{resp: `{"status":"error","message":"The agent x already exists."}`}
	_, err := commit(context.Background(), f, "agent", []change{{Operation: "add"}})
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := err.Error(); !contains(got, "already exists") {
		t.Fatalf("error %q does not carry the service message", got)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (len(sub) == 0 || indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
```

- [ ] **Step 2: Run the test and watch it fail**

Run: `go test ./internal/manage/ -run TestCommit -v`
Expected: build failure, `undefined: commit`

- [ ] **Step 3: Write the implementation**

```go
// Package manage changes what an account owns: creating agents, deleting them,
// regenerating their installation code, and moving them between groups. Reading
// and connecting stay in internal/remote, which every command depends on.
package manage

import (
	"context"
	"encoding/json"
	"fmt"
)

// Executor is the slice of a session this package needs (satisfied by
// *session.Session; a fake stands in for it in tests).
type Executor interface {
	Execute(ctx context.Context, module, command string, params map[string]string) ([]byte, error)
}

// item is one datasource record. It stays untyped because a change carries only
// the fields it touches, and the service echoes back the whole record.
type item map[string]any

// change is one pending edit in a commit.
type change struct {
	Operation string `json:"operation"` // add | update | delete
	Index     int    `json:"index"`
	Item      item   `json:"item"`
}

// commitResponse is what the service returns for operation=commit. A failure
// arrives as status != "ok" with a message, not as an HTTP error.
type commitResponse struct {
	Status       string `json:"status"`
	Message      string `json:"message"`
	ItemsChanged []struct {
		Index int  `json:"index"`
		Item  item `json:"item"`
	} `json:"itemsChanged"`
}

// commit applies changes to a datasource module ("agent" or "group") and
// returns the records the service echoed back — for an add, that is the created
// record, which is where the installation code arrives.
func commit(ctx context.Context, ex Executor, module string, changes []change) ([]item, error) {
	body, err := json.Marshal(changes)
	if err != nil {
		return nil, err
	}
	raw, err := ex.Execute(ctx, module, "datasource", map[string]string{
		"operation": "commit",
		"changes":   string(body),
	})
	if err != nil {
		return nil, err
	}
	var res commitResponse
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("parse %s commit response: %w", module, err)
	}
	if res.Status != "ok" {
		if res.Message != "" {
			return nil, fmt.Errorf("%s", res.Message)
		}
		return nil, fmt.Errorf("%s commit rejected", module)
	}
	out := make([]item, 0, len(res.ItemsChanged))
	for _, c := range res.ItemsChanged {
		out = append(out, c.Item)
	}
	return out, nil
}
```

- [ ] **Step 4: Run the tests and watch them pass**

Run: `go test ./internal/manage/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/manage/datasource.go internal/manage/datasource_test.go
git commit -m "manage: the datasource commit envelope"
```

---

### Task 2: `list` tells a pending agent from an offline one

**Files:**
- Modify: `internal/remote/remote.go` (the `dsItem` struct, `Machine`, and the loop in `List`)
- Modify: `cmd/dwshell/main.go` (the `list` output column)
- Test: `internal/remote/remote_test.go`

**Interfaces:**
- Produces: `Machine.InstallCode int` (0 when there is none) and `Machine.Pending bool`

Verified live: a created-but-uninstalled agent arrives as `"state":"W"`, `"tempCode":281407902`, `"osType":null`. dwshell currently renders it as "Linux offline", which is wrong twice.

- [ ] **Step 1: Write the failing test**

```go
func TestPendingAgentIsNotJustOffline(t *testing.T) {
	it := dsItem{Name: "probe", ID: "A1", State: "W", TempCode: 281407902}
	m := machineFromAgent(it)
	if !m.Pending {
		t.Error("an agent in state W is pending installation")
	}
	if m.Online {
		t.Error("a pending agent is not online")
	}
	if m.InstallCode != 281407902 {
		t.Errorf("InstallCode = %d", m.InstallCode)
	}
}

func TestInstalledAgentIsNotPending(t *testing.T) {
	m := machineFromAgent(dsItem{Name: "GHE", ID: "A2", State: "N", OsType: 0})
	if m.Pending || m.InstallCode != 0 {
		t.Errorf("got %+v", m)
	}
	if !m.Online {
		t.Error("state N is online")
	}
}
```

- [ ] **Step 2: Run the test and watch it fail**

Run: `go test ./internal/remote/ -run Pending -v`
Expected: build failure, `undefined: machineFromAgent`, unknown fields `TempCode`, `Pending`, `InstallCode`

- [ ] **Step 3: Write the implementation**

Add to `dsItem`:

```go
	TempCode int    `json:"tempCode"` // installation code; set only while state is "W"
	IDGroup  string `json:"idGroup"`
```

Add to `Machine`:

```go
	// Pending is an agent created but never installed: it has an InstallCode
	// and no meaningful OS yet (the service sends osType null).
	Pending     bool
	InstallCode int
	IDGroup     string
```

Extract the mapping the `List` loop does today into a function, and use it there:

```go
// machineFromAgent maps one owned-agent record. State "N" is online and "W" is
// created but not yet installed — the state this package's callers show as
// "pending" rather than as an ordinary offline machine.
func machineFromAgent(it dsItem) Machine {
	return Machine{
		Name:        it.Name,
		ID:          it.ID,
		OS:          OS(it.OsType),
		Online:      it.State == "N",
		Pending:     it.State == "W",
		InstallCode: it.TempCode,
		Group:       it.Group,
		IDGroup:     it.IDGroup,
		Apps:        splitApps(it.SupportedApplications),
	}
}
```

In `cmd/dwshell/main.go`, the `list` state column prints `pending` when `m.Pending`, before the online/offline choice, and leaves the OS column empty for a pending agent since the service has not reported one.

- [ ] **Step 4: Run the tests and watch them pass**

Run: `go test ./internal/remote/ ./cmd/dwshell/ -v`
Expected: PASS

- [ ] **Step 5: Verify against the live account**

Run: `go run ./cmd/dwshell list`
Expected: unchanged output for the real agents (no pending agent exists yet; Task 3 rechecks this with one).

- [ ] **Step 6: Commit**

```bash
git add internal/remote/remote.go internal/remote/remote_test.go cmd/dwshell/main.go
git commit -m "remote: tell an agent pending installation from an offline one"
```

---

### Task 3: `dwshell agent create`

**Files:**
- Create: `internal/manage/agent.go`, `cmd/dwshell/agent.go`
- Modify: `cmd/dwshell/main.go` (dispatch + help)
- Test: `internal/manage/agent_test.go`, `cmd/dwshell/agent_test.go`

**Interfaces:**
- Consumes: `commit`, `Executor`, `item`, `change` from Task 1
- Produces:
  ```go
  type Agent struct {
      ID          string
      Name        string
      Description string
      InstallCode int
      State       string
  }
  func CreateAgent(ctx context.Context, ex Executor, name, description, idGroup string) (*Agent, error)
  func FormatCode(code int) string            // cmd/dwshell/agent.go — 281407902 → "281-407-902"
  func InstallInstructions(code int) string   // cmd/dwshell/agent.go
  ```

- [ ] **Step 1: Write the failing tests**

```go
// internal/manage/agent_test.go
func TestCreateAgentReturnsTheInstallationCode(t *testing.T) {
	f := &fakeExec{resp: `{"status":"ok","itemsChanged":[{"index":0,"item":{
		"id":"IXVlyraPmHUOxqNeHzYv","name":"probe","state":"W","tempCode":281407902}}]}`}
	a, err := CreateAgent(context.Background(), f, "probe", "d", "")
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if a.InstallCode != 281407902 || a.ID != "IXVlyraPmHUOxqNeHzYv" || a.State != "W" {
		t.Fatalf("got %+v", a)
	}
	var sent []change
	json.Unmarshal([]byte(f.gotParams["changes"]), &sent)
	if sent[0].Operation != "add" || sent[0].Item["name"] != "probe" {
		t.Fatalf("changes = %+v", sent)
	}
	if _, ok := sent[0].Item["idGroup"]; !ok {
		t.Error("idGroup must be present, null when there is no group")
	}
}
```

```go
// cmd/dwshell/agent_test.go
// The code is shown and typed in groups of three, the way the web client shows
// it and the way the installer forwards it.
func TestFormatCodeGroupsInThrees(t *testing.T) {
	if got := FormatCode(281407902); got != "281-407-902" {
		t.Fatalf("FormatCode = %q, want 281-407-902", got)
	}
}

// tempCode travels as a JSON number, so a leading zero would already be gone;
// padding restores the nine digits the grouping assumes.
func TestFormatCodePadsToNineDigits(t *testing.T) {
	if got := FormatCode(12345678); got != "012-345-678" {
		t.Fatalf("FormatCode = %q, want 012-345-678", got)
	}
}

// the licence constraint is a test, not a habit
func TestInstallInstructionsNeverGiveADownloadAndRunLine(t *testing.T) {
	out := InstallInstructions(281407902)
	if !strings.Contains(out, "https://www.dwservice.net/download.html") {
		t.Error("must point at the download page, where the licence is accepted")
	}
	for _, forbidden := range []string{"curl", "wget", "download/dwagent", "| sh", "|sh"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("must not hand out a download-and-run line, found %q", forbidden)
		}
	}
	if !strings.Contains(out, "-silent key=281-407-902") {
		t.Error("must show the silent-install line with the dashed code")
	}
	if strings.Contains(out, "key=281407902") {
		t.Error("must never hand out the undashed code")
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/manage/ ./cmd/dwshell/ -run 'Create|InstallInstructions' -v`
Expected: `undefined: CreateAgent`, `undefined: InstallInstructions`

- [ ] **Step 3: Write the implementation**

```go
// internal/manage/agent.go
package manage

import (
	"context"
	"fmt"
)

// Agent is an agent record as the service echoes it back after a change.
type Agent struct {
	ID          string
	Name        string
	Description string
	InstallCode int // the installation code; set only while State is "W"
	State       string
}

func agentFromItem(it item) *Agent {
	a := &Agent{}
	if s, ok := it["id"].(string); ok {
		a.ID = s
	}
	if s, ok := it["name"].(string); ok {
		a.Name = s
	}
	if s, ok := it["description"].(string); ok {
		a.Description = s
	}
	if s, ok := it["state"].(string); ok {
		a.State = s
	}
	// JSON numbers decode into float64 through an untyped map; the code is an
	// integer on the wire ("tempCode":281407902).
	if f, ok := it["tempCode"].(float64); ok {
		a.InstallCode = int(f)
	}
	return a
}

// CreateAgent registers a new agent and returns it with its installation code.
// The service mints the code on creation, so this is one round trip. idGroup
// may be empty, which sends a null group.
func CreateAgent(ctx context.Context, ex Executor, name, description, idGroup string) (*Agent, error) {
	it := item{"name": name, "description": description, "idGroup": nil}
	if idGroup != "" {
		it["idGroup"] = idGroup
	}
	items, err := commit(ctx, ex, "agent", []change{{Operation: "add", Index: 0, Item: it}})
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("the service accepted the agent but returned no record")
	}
	return agentFromItem(items[0]), nil
}
```

```go
// cmd/dwshell/agent.go
// InstallInstructions renders what to do with a fresh installation code.
//
// It deliberately does not hand out a download-and-run one-liner. The DWService
// download page is where the licence is accepted ("By selecting the 'Download'
// button I accept the Terms and Conditions…"), and piping the installer
// straight into a shell would route around that. The direct file URLs are known
// and stay unused.
func InstallInstructions(code int) string {
	c := FormatCode(code)
	return fmt.Sprintf(`Installation code: %s

1. Download the agent on the target machine (this is where you accept the licence):
     https://www.dwservice.net/download.html

2. Run the unattended setup there:
     Linux / macOS   sudo sh dwagent.sh -silent key=%s
     Windows         dwagent.exe -silent key=%s
`, c, c, c)
}

// FormatCode renders an installation code the way the web client does and the
// way the installer expects it: three groups of three, dash-separated. The code
// arrives as a JSON number, so it is padded back to nine digits first — a
// leading zero could not have survived the wire.
func FormatCode(code int) string {
	s := fmt.Sprintf("%09d", code)
	return s[0:3] + "-" + s[3:6] + "-" + s[6:]
}
```

The CLI subcommand parses `dwshell agent create <name> [--group G] [--description D] [--json]`, resolves `--group` through Task 7's lookup when given, calls `CreateAgent`, and prints either `InstallInstructions` or, with `--json`, `{"id","name","state","installCode"}`.

- [ ] **Step 4: Run the tests and watch them pass**

Run: `go test ./... `
Expected: PASS

- [ ] **Step 5: Verify against the live account**

```bash
go run ./cmd/dwshell agent create dwshell-probe-2
go run ./cmd/dwshell list | grep probe    # expect: pending, no OS
```

Expected: a code is printed; `list` shows the agent as pending. Leave it in place — Task 4 and Task 8 use it.

- [ ] **Step 6: Commit**

```bash
git add internal/manage cmd/dwshell
git commit -m "agent: create an agent and print its installation code"
```

---

### Task 4: `dwshell agent code`

**Files:**
- Modify: `cmd/dwshell/agent.go`
- Test: `cmd/dwshell/agent_test.go`

**Interfaces:**
- Consumes: `remote.List`, `remote.Resolve`, `Machine.Pending`, `Machine.InstallCode` (Task 2), `InstallInstructions` (Task 3)
- Produces: nothing later tasks depend on

Reading a pending agent's code needs no new protocol: it arrives in the listing `list` already fetches.

- [ ] **Step 1: Write the failing test**

```go
func TestAgentCodeRefusesAnInstalledAgent(t *testing.T) {
	m := remote.Machine{Name: "GHE", Online: true}
	err := agentCodeFor(&m)
	if err == nil {
		t.Fatal("an installed agent has no installation code")
	}
	if !strings.Contains(err.Error(), "reinstall") {
		t.Errorf("the error should point at `agent reinstall`, got %q", err)
	}
}

func TestAgentCodeRefusesAShare(t *testing.T) {
	m := remote.Machine{Name: "Regia", Shared: true, Pending: true, InstallCode: 1}
	if err := agentCodeFor(&m); err == nil || !strings.Contains(err.Error(), "share") {
		t.Fatalf("a share is someone else's agent, got %v", err)
	}
}
```

- [ ] **Step 2: Run the test and watch it fail**

Run: `go test ./cmd/dwshell/ -run AgentCode -v`
Expected: `undefined: agentCodeFor`

- [ ] **Step 3: Write the implementation**

```go
// agentCodeFor reports why an agent has no installation code to show, or nil
// when it does. A code exists only between creation and installation.
func agentCodeFor(m *remote.Machine) error {
	if m.Shared {
		return fmt.Errorf("%s is a share — someone else's agent — so it has no installation code here", m.Name)
	}
	if !m.Pending {
		return fmt.Errorf("%s is already installed; `dwshell agent reinstall %s` mints a new code", m.Name, m.Name)
	}
	return nil
}
```

The subcommand resolves the agent, calls `agentCodeFor`, and prints `InstallInstructions(m.InstallCode)` or the `--json` object.

- [ ] **Step 4: Run the tests and watch them pass**

Run: `go test ./cmd/dwshell/ -v`
Expected: PASS

- [ ] **Step 5: Verify against the live account**

```bash
go run ./cmd/dwshell agent code dwshell-probe-2   # same code as Task 3 printed
go run ./cmd/dwshell agent code GHE               # refuses, points at reinstall
```

- [ ] **Step 6: Commit**

```bash
git add cmd/dwshell
git commit -m "agent: read back the code of an agent pending installation"
```

---

### Task 5: `dwshell agent rm`, with its confirmation

**Files:**
- Modify: `internal/manage/agent.go`, `cmd/dwshell/agent.go`
- Test: `internal/manage/agent_test.go`, `cmd/dwshell/agent_test.go`

**Interfaces:**
- Produces:
  ```go
  func DeleteAgent(ctx context.Context, ex Executor, id string) error
  func confirm(prompt string, assumeYes, interactive bool) error   // cmd/dwshell
  ```

Verified live: `changes=[{"operation":"delete","index":0,"item":{"_id":ID,"id":ID}}]`.

- [ ] **Step 1: Write the failing tests**

```go
// internal/manage/agent_test.go
func TestDeleteAgentSendsBothIDForms(t *testing.T) {
	f := &fakeExec{resp: `{"status":"ok","itemsChanged":[]}`}
	if err := DeleteAgent(context.Background(), f, "A1"); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}
	var sent []change
	json.Unmarshal([]byte(f.gotParams["changes"]), &sent)
	if sent[0].Operation != "delete" || sent[0].Item["_id"] != "A1" || sent[0].Item["id"] != "A1" {
		t.Fatalf("changes = %+v", sent)
	}
}
```

```go
// cmd/dwshell/agent_test.go — automation must not delete a machine by accident
func TestConfirmRefusesWithoutATerminal(t *testing.T) {
	if err := confirm("delete agent x?", false, false); err == nil {
		t.Fatal("with no terminal and no --yes it must refuse")
	} else if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("the error should name --yes, got %q", err)
	}
}

func TestConfirmPassesWithYes(t *testing.T) {
	if err := confirm("delete agent x?", true, false); err != nil {
		t.Fatalf("--yes must proceed without a terminal: %v", err)
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/manage/ ./cmd/dwshell/ -run 'Delete|Confirm' -v`
Expected: `undefined: DeleteAgent`, `undefined: confirm`

- [ ] **Step 3: Write the implementation**

```go
// DeleteAgent removes an agent from the account. The service wants the record's
// id under both keys it uses internally.
func DeleteAgent(ctx context.Context, ex Executor, id string) error {
	_, err := commit(ctx, ex, "agent", []change{{
		Operation: "delete", Index: 0, Item: item{"_id": id, "id": id},
	}})
	return err
}
```

```go
// confirm gates an irreversible change. With a terminal it asks; without one —
// a script, CI — it refuses unless the caller passed --yes, so automation
// cannot delete a machine by accident.
func confirm(prompt string, assumeYes, interactive bool) error {
	if assumeYes {
		return nil
	}
	if !interactive {
		return fmt.Errorf("%s: refusing without a terminal; pass --yes to proceed", prompt)
	}
	fmt.Fprintf(os.Stderr, "%s [y/N] ", prompt)
	var answer string
	fmt.Fscanln(os.Stdin, &answer)
	if answer != "y" && answer != "Y" {
		return fmt.Errorf("cancelled")
	}
	return nil
}
```

The subcommand resolves the agent, refuses a share, confirms with `delete agent "<name>"?`, then calls `DeleteAgent`. Interactivity comes from the same terminal check `term` already uses.

- [ ] **Step 4: Run the tests and watch them pass**

Run: `go test ./... `
Expected: PASS

- [ ] **Step 5: Verify against the live account**

```bash
go run ./cmd/dwshell agent rm dwshell-probe-2 < /dev/null   # refuses: no terminal, no --yes
go run ./cmd/dwshell agent create dwshell-probe-3
go run ./cmd/dwshell agent rm dwshell-probe-3 --yes
go run ./cmd/dwshell list | grep -c probe-3                 # expect 0
```

- [ ] **Step 6: Commit**

```bash
git add internal/manage cmd/dwshell
git commit -m "agent: delete an agent, behind a confirmation"
```

---

### Task 6: `dwshell agent reinstall`

**Files:**
- Modify: `internal/manage/agent.go`, `cmd/dwshell/agent.go`
- Test: `internal/manage/agent_test.go`

**Interfaces:**
- Produces: `func ReinstallAgent(ctx context.Context, ex Executor, id string) error`

`module=agent command=reinstall parameter id=<agentId>` is read from the client but not yet exercised; Step 5 confirms it live.

- [ ] **Step 1: Write the failing test**

```go
func TestReinstallAgentCallsTheReinstallCommand(t *testing.T) {
	f := &fakeExec{resp: `K`}
	if err := ReinstallAgent(context.Background(), f, "A1"); err != nil {
		t.Fatalf("ReinstallAgent: %v", err)
	}
	if f.gotModule != "agent" || f.gotCommand != "reinstall" || f.gotParams["id"] != "A1" {
		t.Fatalf("sent %s/%s %v", f.gotModule, f.gotCommand, f.gotParams)
	}
}
```

- [ ] **Step 2: Run the test and watch it fail**

Run: `go test ./internal/manage/ -run Reinstall -v`
Expected: `undefined: ReinstallAgent`

- [ ] **Step 3: Write the implementation**

```go
// ReinstallAgent puts an installed agent back into "pending installation" with
// a fresh code, invalidating the previous one. Read the new code with
// remote.List afterwards: this command answers with a bare acknowledgement.
func ReinstallAgent(ctx context.Context, ex Executor, id string) error {
	_, err := ex.Execute(ctx, "agent", "reinstall", map[string]string{"id": id})
	return err
}
```

The subcommand confirms (it invalidates the existing code) exactly as `rm` does, then re-reads the listing and prints the new code through `InstallInstructions`.

- [ ] **Step 4: Run the tests and watch them pass**

Run: `go test ./... `
Expected: PASS

- [ ] **Step 5: Confirm the call against the live service**

```bash
go run ./cmd/dwshell agent create dwshell-probe-4
go run ./cmd/dwshell agent reinstall dwshell-probe-4 --yes
go run ./cmd/dwshell agent code dwshell-probe-4     # expect a different code
go run ./cmd/dwshell agent rm dwshell-probe-4 --yes
```

If `reinstall` rejects an agent that is already pending, note it and confirm instead against a throwaway agent that was installed in Task 8; do not run it against GHE, which is a working machine.

- [ ] **Step 6: Commit**

```bash
git add internal/manage cmd/dwshell
git commit -m "agent: regenerate an installation code"
```

---

### Task 7: Groups

**Files:**
- Create: `internal/manage/group.go`
- Modify: `internal/manage/agent.go`, `cmd/dwshell/agent.go`
- Test: `internal/manage/group_test.go`

**Interfaces:**
- Produces:
  ```go
  type Group struct{ ID, Name string }
  func ListGroups(ctx context.Context, ex Executor) ([]Group, error)
  func ResolveGroup(groups []Group, name string) (*Group, error)
  func SetAgentGroup(ctx context.Context, ex Executor, agentID, idGroup string) error
  ```

Groups must already exist: a typo should be an error, not a new object on the account.

- [ ] **Step 1: Write the failing tests**

```go
func TestResolveGroupIsExactAndListsOnMiss(t *testing.T) {
	gs := []Group{{ID: "G1", Name: "prod"}, {ID: "G2", Name: "lab"}}
	g, err := ResolveGroup(gs, "lab")
	if err != nil || g.ID != "G2" {
		t.Fatalf("got %+v err=%v", g, err)
	}
	_, err = ResolveGroup(gs, "nope")
	if err == nil {
		t.Fatal("an unknown group must fail")
	}
	for _, want := range []string{"prod", "lab"} {
		if !contains(err.Error(), want) {
			t.Errorf("the error should list existing groups, missing %q in %q", want, err)
		}
	}
}

func TestSetAgentGroupUpdatesOnlyTheGroup(t *testing.T) {
	f := &fakeExec{resp: `{"status":"ok","itemsChanged":[]}`}
	if err := SetAgentGroup(context.Background(), f, "A1", "G2"); err != nil {
		t.Fatalf("SetAgentGroup: %v", err)
	}
	var sent []change
	json.Unmarshal([]byte(f.gotParams["changes"]), &sent)
	if sent[0].Operation != "update" || sent[0].Item["idGroup"] != "G2" || sent[0].Item["id"] != "A1" {
		t.Fatalf("changes = %+v", sent)
	}
}

func TestSetAgentGroupClearsWithAnEmptyID(t *testing.T) {
	f := &fakeExec{resp: `{"status":"ok","itemsChanged":[]}`}
	SetAgentGroup(context.Background(), f, "A1", "")
	var sent []change
	json.Unmarshal([]byte(f.gotParams["changes"]), &sent)
	if sent[0].Item["idGroup"] != nil {
		t.Fatalf("removing from a group sends null, got %#v", sent[0].Item["idGroup"])
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/manage/ -run Group -v`
Expected: `undefined: ResolveGroup`, `undefined: SetAgentGroup`

- [ ] **Step 3: Write the implementation**

```go
// internal/manage/group.go
package manage

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Group is an agent group on the account.
type Group struct{ ID, Name string }

// ListGroups reads the account's groups. Same datasource shape as agents.
func ListGroups(ctx context.Context, ex Executor) ([]Group, error) {
	raw, err := ex.Execute(ctx, "group", "datasource", map[string]string{"operation": "load"})
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	var res struct {
		Items []struct {
			ID   string `json:"_id"`
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("parse groups: %w", err)
	}
	out := make([]Group, 0, len(res.Items))
	for _, it := range res.Items {
		out = append(out, Group{ID: it.ID, Name: it.Name})
	}
	return out, nil
}

// ResolveGroup matches a group by exact name. Creating groups is out of scope,
// so an unknown name is an error that shows what does exist — a typo should not
// quietly become a new group.
func ResolveGroup(groups []Group, name string) (*Group, error) {
	for i := range groups {
		if groups[i].Name == name {
			return &groups[i], nil
		}
	}
	names := make([]string, 0, len(groups))
	for _, g := range groups {
		names = append(names, g.Name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, fmt.Errorf("no group named %q; this account has no groups", name)
	}
	return nil, fmt.Errorf("no group named %q; existing groups: %s", name, strings.Join(names, ", "))
}
```

```go
// internal/manage/agent.go
// SetAgentGroup moves an agent into a group, or out of every group when idGroup
// is empty. Only the group field is sent; the service keeps the rest.
func SetAgentGroup(ctx context.Context, ex Executor, agentID, idGroup string) error {
	it := item{"_id": agentID, "id": agentID, "idGroup": nil}
	if idGroup != "" {
		it["idGroup"] = idGroup
	}
	_, err := commit(ctx, ex, "agent", []change{{Operation: "update", Index: 0, Item: it}})
	return err
}
```

The subcommand is `dwshell agent group <agent> <group>` and `dwshell agent group <agent> --none`.

- [ ] **Step 4: Run the tests and watch them pass**

Run: `go test ./... `
Expected: PASS

- [ ] **Step 5: Verify against the live account**

```bash
go run ./cmd/dwshell agent create dwshell-probe-5
go run ./cmd/dwshell agent group dwshell-probe-5 nonexistent-group   # lists real groups
go run ./cmd/dwshell agent group dwshell-probe-5 <a real group>
go run ./cmd/dwshell list | grep probe-5
go run ./cmd/dwshell agent group dwshell-probe-5 --none
go run ./cmd/dwshell agent rm dwshell-probe-5 --yes
```

If the account has no group, create one in the web client first; `manage` does not create groups.

- [ ] **Step 6: Commit**

```bash
git add internal/manage cmd/dwshell
git commit -m "agent: move an agent between groups"
```

---

### Task 8: Live validation of the unattended install, then docs

**Files:**
- Modify: `README.md`, `docs/PROTOCOL.md`

The point of the feature is an installation that really works, so the docs are written after it has been seen to work.

- [ ] **Step 1: Linux, in an ephemeral container**

```bash
CODE=$(go run ./cmd/dwshell agent create dwshell-probe-linux --json | python3 -c 'import json,sys; print(json.load(sys.stdin)["installCode"])')
docker run --rm -it debian:12 bash -c "apt-get update -qq && apt-get install -y -qq curl >/dev/null &&
  curl -fsSL -o /tmp/dwagent.sh https://www.dwservice.net/download/dwagent.sh &&
  sh /tmp/dwagent.sh -silent key=$CODE; sleep 30; echo done"
```

Fetching the installer here is a test of the documented run line, not the line dwshell prints — dwshell still points a user at the download page.

Expected: `dwshell list` shows `dwshell-probe-linux` leaving `pending`. Record what the state becomes. Then `go run ./cmd/dwshell agent rm dwshell-probe-linux --yes`.

- [ ] **Step 2: macOS, on this workstation**

```bash
CODE=$(go run ./cmd/dwshell agent create dwshell-probe-mac --json | python3 -c 'import json,sys; print(json.load(sys.stdin)["installCode"])')
# download the agent from https://www.dwservice.net/download.html by hand, accepting the licence
sudo sh ~/Downloads/dwagent.sh -silent key=$CODE
go run ./cmd/dwshell list | grep probe-mac
```

Then uninstall and clean up:

```bash
sudo sh ~/Downloads/dwagent.sh uninstall
go run ./cmd/dwshell agent rm dwshell-probe-mac --yes
```

Expected: the agent appears installed while it runs, and nothing is left on the machine or the account afterwards.

- [ ] **Step 3: Write the docs from what happened**

`README.md` gains an `agent` section: the subcommands, the two-step installation (page, then run line), the note that groups must already exist, and the confirmation behaviour. `docs/PROTOCOL.md` gains the datasource commit envelope, the `tempCode`/`state` fields, and `agent/reinstall`, marked with what was verified live and what was read from the client.

The Windows run line is documented as derived from the installer's source and **not** executed — see §9 of the spec.

- [ ] **Step 4: Full verification**

Run: `gofmt -l cmd internal && go vet ./... && go test -race ./...`
Expected: no output from gofmt, PASS from the rest.

- [ ] **Step 5: Confirm the account is clean**

Run: `go run ./cmd/dwshell list | grep -c probe`
Expected: `0`

- [ ] **Step 6: Commit**

```bash
git add README.md docs/PROTOCOL.md
git commit -m "docs: agent management and the datasource protocol"
```

---

## Self-review

**Spec coverage.** §3 protocol → Tasks 1, 3, 5, 6, 7. §4 installation and the licence constraint → Task 3 (with a test that forbids a download-and-run line) and Task 8. §5 command surface → Tasks 3–7; `--json` appears in Task 3's CLI step and is carried by each subcommand. §5 pending state in `list` → Task 2. §6 structure → the file table. §7 errors: share refused → Task 4; unknown group → Task 7; already installed → Task 4; duplicate name → Task 1 surfaces the service's message. §8 testing → each task's Step 5, plus Task 8.

**Placeholders.** None: every step carries the code or the command it needs.

**Type consistency.** `Executor`, `item`, `change`, `commit` are defined in Task 1 and used unchanged after. `Agent.InstallCode`, `Machine.InstallCode`, `FormatCode(code int)` and `InstallInstructions(code int)` are all `int`, matching the wire, where `tempCode` is a number; the dashed string exists only at the edge, where it is printed or passed to `key=`. `ResolveGroup` returns `*Group` whose `ID` feeds `SetAgentGroup`'s `idGroup`.

**Gap found and closed while reviewing:** the spec says `agent create` accepts `--group`, which needs Task 7's `ResolveGroup`. Task 3 is implemented before Task 7, so its `--group` flag is wired in Task 7's CLI step; Task 3's own live check uses no group.
