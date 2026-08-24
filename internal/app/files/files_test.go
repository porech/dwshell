package files

import "testing"

func TestParseList(t *testing.T) {
	raw := []byte(`{"items":[
		{"Name":"D:bin","LastModified":1787510458498,"Rights":"755","Owner":"root","Group":"root"},
		{"Name":"F:hosts","LastModified":1688578959000,"Length":221,"Rights":"644","Owner":"root","Group":"root"},
		{"Name":"F:vmlinuz"}
	],"permissions":{"apps":{}}}`)

	entries, err := parseList(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries", len(entries))
	}

	if d := entries[0]; !d.IsDir || d.Name != "bin" || d.Rights != "755" {
		t.Fatalf("dir entry wrong: %+v", d)
	}
	if d := entries[0]; d.ModTime.IsZero() {
		t.Fatal("dir should have a mod time")
	}

	f := entries[1]
	if f.IsDir || f.Name != "hosts" || f.Size != 221 || f.Owner != "root" {
		t.Fatalf("file entry wrong: %+v", f)
	}

	// A bare "F:name" with no metadata still parses as a zero-value file.
	if v := entries[2]; v.IsDir || v.Name != "vmlinuz" || v.Size != 0 || !v.ModTime.IsZero() {
		t.Fatalf("bare file entry wrong: %+v", v)
	}
}
