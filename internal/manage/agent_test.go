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

func TestDeleteAgentSendsBothIDForms(t *testing.T) {
	f := &fakeExec{resp: `{"status":"ok","itemsChanged":[]}`}
	if err := DeleteAgent(context.Background(), f, "A1"); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}
	sent := sentChanges(t, f)
	if sent[0].Operation != "delete" || sent[0].Item["_id"] != "A1" || sent[0].Item["id"] != "A1" {
		t.Fatalf("changes = %+v", sent)
	}
}

// The service rejects a partial update with a NullPointerException: a change
// has to carry the whole record with the one field altered, which is what the
// browser client sends. So the record is read back before it is written.
func TestSetAgentGroupSendsTheWholeRecord(t *testing.T) {
	f := &fakeExec{resp: `{"items":[
		{"id":"A0","_id":"A0","name":"other","idGroup":null},
		{"id":"A1","_id":"A1","name":"probe","description":"d","state":"W","idGroup":null}]}`}
	// the load answers first; the commit reuses the same canned body, which is
	// enough to inspect what was sent
	_ = SetAgentGroup(context.Background(), f, "A1", "G2")

	sent := sentChanges(t, f)
	if sent[0].Operation != "update" {
		t.Fatalf("operation = %q", sent[0].Operation)
	}
	if sent[0].Item["idGroup"] != "G2" {
		t.Errorf("idGroup = %#v, want G2", sent[0].Item["idGroup"])
	}
	if sent[0].Item["name"] != "probe" {
		t.Errorf("the whole record must travel, name = %#v", sent[0].Item["name"])
	}
	if sent[0].Index != 1 {
		t.Errorf("index = %d, want the record's position 1", sent[0].Index)
	}
}

func TestSetAgentGroupClearsWithAnEmptyID(t *testing.T) {
	f := &fakeExec{resp: `{"items":[{"id":"A1","_id":"A1","name":"probe","idGroup":"G2"}]}`}
	_ = SetAgentGroup(context.Background(), f, "A1", "")
	if got := sentChanges(t, f)[0].Item["idGroup"]; got != nil {
		t.Fatalf("removing from a group sends null, got %#v", got)
	}
}

func TestSetAgentGroupFailsOnAnUnknownAgent(t *testing.T) {
	f := &fakeExec{resp: `{"items":[{"id":"A1","_id":"A1","name":"probe"}]}`}
	if err := SetAgentGroup(context.Background(), f, "NOPE", "G2"); err == nil {
		t.Fatal("expected an error for an id that is not in the listing")
	}
}
