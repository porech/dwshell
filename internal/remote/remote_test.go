package remote

import "testing"

func machines() []Machine {
	return []Machine{
		{Name: "GHE", ID: "id-ghe", OS: OSLinux, Online: true},
		{Name: "Regia", ID: "idagent-regia", ShareID: "SH1", OS: OSWindows, Online: true, Shared: true},
		{Name: "dup", ID: "id-a", Online: true},
		{Name: "dup", ID: "id-b", Online: true, Shared: true},
	}
}

func TestResolveByName(t *testing.T) {
	m, err := Resolve(machines(), "GHE", Any)
	if err != nil || m.ID != "id-ghe" {
		t.Fatalf("got %+v err=%v", m, err)
	}
}

func TestResolveByID(t *testing.T) {
	m, err := Resolve(machines(), "idagent-regia", Any)
	if err != nil || m.Name != "Regia" {
		t.Fatalf("got %+v err=%v", m, err)
	}
}

func TestResolveAmbiguous(t *testing.T) {
	_, err := Resolve(machines(), "dup", Any)
	if _, ok := err.(*ErrAmbiguous); !ok {
		t.Fatalf("want ErrAmbiguous, got %v", err)
	}
}

func TestResolveFilterDisambiguates(t *testing.T) {
	m, err := Resolve(machines(), "dup", OwnedOnly)
	if err != nil || m.ID != "id-a" {
		t.Fatalf("owned: got %+v err=%v", m, err)
	}
	m, err = Resolve(machines(), "dup", SharedOnly)
	if err != nil || m.ID != "id-b" {
		t.Fatalf("shared: got %+v err=%v", m, err)
	}
}

func TestResolveNotFound(t *testing.T) {
	_, err := Resolve(machines(), "nope", Any)
	if _, ok := err.(*ErrNotFound); !ok {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestOS(t *testing.T) {
	if !OSLinux.IsUnix() || !OSMac.IsUnix() || OSWindows.IsUnix() {
		t.Fatal("IsUnix mapping wrong")
	}
	if OSWindows.String() != "Windows" || OSLinux.String() != "Linux" {
		t.Fatal("String mapping wrong")
	}
}

func TestSupportsShell(t *testing.T) {
	if !(Machine{Apps: []string{"desktop", "shell"}}).SupportsShell() {
		t.Fatal("should support shell")
	}
	if (Machine{Apps: []string{"desktop"}}).SupportsShell() {
		t.Fatal("should not support shell")
	}
	if !(Machine{Apps: nil}).SupportsShell() {
		t.Fatal("empty apps (share full access) should allow shell")
	}
}
