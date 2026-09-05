package manage

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// fakeExec records what was sent and replays a canned response.
type fakeExec struct {
	gotModule, gotCommand string
	gotParams             map[string]string
	resp                  string
	err                   error
}

func (f *fakeExec) Execute(_ context.Context, module, command string, p map[string]string) ([]byte, error) {
	f.gotModule, f.gotCommand, f.gotParams = module, command, p
	return []byte(f.resp), f.err
}

// sentChanges decodes the changes envelope the fake received.
func sentChanges(t *testing.T, f *fakeExec) []change {
	t.Helper()
	var sent []change
	if err := json.Unmarshal([]byte(f.gotParams["changes"]), &sent); err != nil {
		t.Fatalf("changes is not JSON: %v", err)
	}
	return sent
}

func TestCommitSendsTheChangesEnvelope(t *testing.T) {
	f := &fakeExec{resp: `{"status":"ok","itemsChanged":[{"index":0,"item":{"id":"A1","tempCode":281407902}}]}`}
	items, err := commit(context.Background(), f, "agent",
		[]change{{Operation: "add", Index: 0, Item: item{"name": "n1"}}})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if f.gotModule != "agent" || f.gotCommand != "datasource" {
		t.Fatalf("sent to %s/%s, want agent/datasource", f.gotModule, f.gotCommand)
	}
	if f.gotParams["operation"] != "commit" {
		t.Fatalf("operation = %q, want commit", f.gotParams["operation"])
	}
	sent := sentChanges(t, f)
	if len(sent) != 1 || sent[0].Operation != "add" || sent[0].Item["name"] != "n1" {
		t.Fatalf("changes = %+v", sent)
	}
	if len(items) != 1 || items[0]["id"] != "A1" {
		t.Fatalf("items = %+v", items)
	}
}

// The service reports a rejection in the body, with a successful HTTP status,
// so the message has to be dug out rather than surfaced as a transport error.
func TestCommitSurfacesTheServiceMessage(t *testing.T) {
	f := &fakeExec{resp: `{"status":"error","message":"The agent x already exists."}`}
	_, err := commit(context.Background(), f, "agent", []change{{Operation: "add"}})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error %q does not carry the service message", err)
	}
}

func TestCommitReportsARejectionWithNoMessage(t *testing.T) {
	f := &fakeExec{resp: `{"status":"error"}`}
	if _, err := commit(context.Background(), f, "group", []change{{Operation: "add"}}); err == nil {
		t.Fatal("a non-ok status must be an error even with no message")
	}
}
