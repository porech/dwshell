package manage

import (
	"context"
	"strings"
	"testing"
)

func TestListGroupsReadsIDAndName(t *testing.T) {
	f := &fakeExec{resp: `{"items":[{"_id":"G1","name":"prod"},{"_id":"G2","name":"lab"}]}`}
	gs, err := ListGroups(context.Background(), f)
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	if f.gotModule != "group" || f.gotParams["operation"] != "load" {
		t.Fatalf("sent %s %v", f.gotModule, f.gotParams)
	}
	if len(gs) != 2 || gs[1].ID != "G2" || gs[1].Name != "lab" {
		t.Fatalf("groups = %+v", gs)
	}
}

func TestResolveGroupIsExactAndListsOnMiss(t *testing.T) {
	gs := []Group{{ID: "G1", Name: "prod"}, {ID: "G2", Name: "lab"}}
	g, err := ResolveGroup(gs, "lab")
	if err != nil || g.ID != "G2" {
		t.Fatalf("got %+v err=%v", g, err)
	}
	_, err = ResolveGroup(gs, "nope")
	if err == nil {
		t.Fatal("an unknown group must fail rather than be created")
	}
	for _, want := range []string{"prod", "lab"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should list existing groups, missing %q in %q", want, err)
		}
	}
}

func TestResolveGroupSaysWhenThereAreNone(t *testing.T) {
	_, err := ResolveGroup(nil, "prod")
	if err == nil || !strings.Contains(err.Error(), "no groups") {
		t.Fatalf("got %v", err)
	}
}
