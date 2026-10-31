package transport

import (
	"bytes"
	"io"
	"os"
	"testing"
	"time"
)

func TestProcessPeer(t *testing.T) {
	if os.Args[len(os.Args)-1] != "pencode-pipe-peer" {
		return
	}
	_, _ = io.Copy(os.Stdout, os.Stdin)
	os.Exit(0)
}

func TestProcessOwnsPipesUntilOutputIsConsumed(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := Start(executable, "-test.run=^TestProcessPeer$", "--", "pencode-pipe-peer")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	p := conn.(*process)
	want := "binary\x00中文\xff\n"
	if _, err := io.WriteString(p, want); err != nil {
		t.Fatal(err)
	}
	if err := p.writer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		t.Fatal("child was not reaped")
	}
	data, err := io.ReadAll(p)
	if err != nil || string(data) != want {
		t.Fatalf("output after child exit = %q, %v", data, err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal("close was not idempotent", err)
	}
}

func TestClosingPeer(t *testing.T) {
	if os.Args[len(os.Args)-1] != "pencode-closing-peer" {
		return
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	if _, err := os.Stdout.Write(bytes.Repeat([]byte{'x'}, 1<<20)); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}
func TestProcessCloseDrainsPeerOutput(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := Start(executable, "-test.run=^TestClosingPeer$", "--", "pencode-closing-peer")
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := conn.(*process).Wait(); err != nil {
		t.Fatal("server was killed before graceful cleanup", err)
	}
}

func TestWorkspacePeerCloseDrainsOutput(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := StartPeerIn(t.TempDir(), executable, io.Discard, "-test.run=^TestClosingPeer$", "--", "pencode-closing-peer")
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := conn.(*process).Wait(); err != nil {
		t.Fatal("workspace peer was killed before graceful cleanup", err)
	}
}
