package manage

import (
	"context"
	"testing"
)

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
	sent := sentChanges(t, f)
	if sent[0].Operation != "add" || sent[0].Item["name"] != "probe" {
		t.Fatalf("changes = %+v", sent)
	}
	if _, ok := sent[0].Item["idGroup"]; !ok {
		t.Error("idGroup must be present, null when there is no group")
	}
}

func TestCreateAgentSendsTheGroupWhenGiven(t *testing.T) {
	f := &fakeExec{resp: `{"status":"ok","itemsChanged":[{"index":0,"item":{"id":"A1"}}]}`}
	if _, err := CreateAgent(context.Background(), f, "n", "", "G2"); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if got := sentChanges(t, f)[0].Item["idGroup"]; got != "G2" {
		t.Fatalf("idGroup = %#v, want G2", got)
	}
}

// A creation the service accepted but answered with nothing would leave the
// caller with no code to show, which is worse than an error.
func TestCreateAgentFailsWhenNoRecordComesBack(t *testing.T) {
	f := &fakeExec{resp: `{"status":"ok","itemsChanged":[]}`}
	if _, err := CreateAgent(context.Background(), f, "n", "", ""); err == nil {
		t.Fatal("expected an error when the service returns no record")
	}
}
