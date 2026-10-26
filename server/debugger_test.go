package server

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/pencode-labs/pencode/peb"
	"github.com/pencode-labs/pencode/protocol"
	"github.com/pencode-labs/pencode/server/extensionhost"
)

func TestDisablingExtensionCancelsAdapterStartup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	directory := t.TempDir()
	marker := filepath.Join(directory, "started")
	if err := os.MkdirAll(filepath.Join(directory, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(directory, "bin", "launcher")
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nprintf ready > \"$1\"\nexec /bin/sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	s := newServices(ctx, Options{ExtensionDirectory: directory})
	defer s.close()
	s.roots[directory] = true
	lifetime, revoke := context.WithCancel(ctx)
	s.extensions.entries[1] = &extensionEntry{root: root, committed: true, ctx: lifetime, cancel: revoke, registration: extensionhost.Registration{
		Info:          extensionhost.Installed{Path: directory},
		Contributions: peb.Contributions{DebugAdapters: []peb.DebugAdapter{{ID: "Delve", Label: "fixture", Command: "bin/launcher", Args: []string{marker}}}},
	}}
	var request protocol.Encoder
	request.String("Delve")
	request.String("")
	request.String("")
	request.U32(0)
	done := make(chan error, 1)
	go func() {
		decoder := protocol.Decoder{Buffer: request.Buffer}
		var response protocol.Encoder
		done <- s.openDebugAdapter(ctx, directory, &decoder, &response)
	}()
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("adapter did not start", ctx.Err())
		case err := <-done:
			t.Fatal("startup ended early", err)
		case <-time.After(time.Millisecond):
		}
	}
	revoked := make(chan struct{})
	go func() {
		s.extensions.mu.Lock()
		s.extensions.remove(1, s.extensions.entries[1])
		s.extensions.mu.Unlock()
		close(revoked)
	}()
	select {
	case <-revoked:
	case <-time.After(time.Second):
		t.Fatal("adapter startup blocked extension disable")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("revoked adapter startup succeeded")
		}
	case <-ctx.Done():
		t.Fatal("revoked startup was not canceled")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.streams) != 0 {
		t.Fatal("revoked adapter left streams")
	}
}
