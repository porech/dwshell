package term

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

type diagnosticIdleRemote struct{ out chan []byte }

func (r *diagnosticIdleRemote) Input(string) error    { return nil }
func (r *diagnosticIdleRemote) Resize(int, int) error { return nil }
func (r *diagnosticIdleRemote) Output() <-chan []byte { return r.out }

func TestStdinEOFDisconnects(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = writer.Close() // immediate real EOF on a non-interactive stdin
	original := os.Stdin
	os.Stdin = reader
	defer func() { os.Stdin = original; reader.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = Bridge(ctx, &diagnosticIdleRemote{out: make(chan []byte)})
	if err != nil {
		t.Fatalf("stdin EOF should disconnect cleanly before context deadline, got %v", err)
	}
	t.Log("Real stdin EOF disconnected cleanly")
}

type failingInputRemote struct {
	diagnosticIdleRemote
	err error
}

func (r *failingInputRemote) Input(string) error { return r.err }

func TestStdinInputFailureDisconnects(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	if _, err := writer.Write([]byte("pwd\n")); err != nil {
		t.Fatal(err)
	}
	original := os.Stdin
	os.Stdin = reader
	defer func() { os.Stdin = original }()
	want := errors.New("input transport failed")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = Bridge(ctx, &failingInputRemote{diagnosticIdleRemote{out: make(chan []byte)}, want})
	if !errors.Is(err, want) {
		t.Fatalf("expected input failure to leave Bridge, got %v", err)
	}
}

func TestStdinReadFailureDisconnects(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = reader.Close()
	_ = writer.Close()
	original := os.Stdin
	os.Stdin = reader
	defer func() { os.Stdin = original }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = Bridge(ctx, &diagnosticIdleRemote{out: make(chan []byte)})
	if !errors.Is(err, os.ErrClosed) {
		t.Fatalf("expected closed stdin error to leave Bridge, got %v", err)
	}
}
