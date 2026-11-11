package client

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pencode-labs/pencode/protocol"
)

func TestDocumentPagesFlushEditsWithoutSaving(t *testing.T) {
	c, w := peer(t, nil)
	original := strings.Repeat("entry 中文🙂\n", protocol.TextPagingThreshold/len("entry 中文🙂\n")+1000)
	name := filepath.Join(w.Path, "large.txt")
	if err := os.WriteFile(name, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	doc, first, err := c.OpenDocument(t.Context(), w.ID, "large.txt", protocol.DocumentText)
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Close()
	if !doc.Page().Paged {
		t.Fatal("large file was loaded in full")
	}
	updated := "first edit\n" + first
	doc.Update(first, updated, 0, 0, len("first edit\n"))
	second, page, err := doc.ReadPage(t.Context(), 100000)
	if err != nil || page.Offset == 0 {
		t.Fatal(page, err)
	}
	updatedSecond := "SECOND\n" + second
	doc.Update(second, updatedSecond, 0, 0, len("SECOND\n"))
	third, _, err := doc.ReadPage(t.Context(), 200000)
	if err != nil {
		t.Fatal(err)
	}
	disk, _ := os.ReadFile(name)
	if string(disk) != original {
		t.Fatal("page navigation saved without permission")
	}
	if err := doc.SaveSnapshot(t.Context(), third); err != nil {
		t.Fatal(err)
	}
	disk, err = os.ReadFile(name)
	want := "first edit\n" + original
	want = want[:page.Offset] + "SECOND\n" + want[page.Offset:]
	if err != nil || string(disk) != want {
		t.Fatal("save lost off-page edits or unloaded text", err)
	}
	// A cancelled viewport request leaves the current page editable.
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := doc.ReadPage(cancelled, 0); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	firstAgain, page, err := doc.ReadPage(t.Context(), 0)
	if err != nil || page.Offset != 0 || !strings.HasPrefix(firstAgain, "first edit\n") {
		t.Fatal("revisit lost edit", err)
	}
}

func TestCancelledPageReadDoesNotRetargetEdits(t *testing.T) {
	var gate *gated
	c, w := peer(t, func(conn net.Conn) io.ReadWriteCloser {
		gate = &gated{Conn: conn, entered: make(chan struct{}), release: make(chan struct{})}
		return gate
	})
	unblock := sync.OnceFunc(func() { close(gate.release) })
	defer unblock()
	original := strings.Repeat("line\n", protocol.TextPagingThreshold/5+1000)
	name := filepath.Join(w.Path, "large.txt")
	if err := os.WriteFile(name, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	doc, first, err := c.OpenDocument(t.Context(), w.ID, "large.txt", protocol.DocumentText)
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Release()
	gate.pause.Store(true)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, _, err := doc.ReadPage(ctx, 200000); finished <- err }()
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("page read did not begin")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("page cancellation blocked")
	}
	unblock()
	if doc.Page().Offset != 0 {
		t.Fatal("cancelled response replaced the client page")
	}
	updated := "updated\n" + first
	doc.Update(first, updated, 0, 0, len("updated\n"))
	if err := doc.SaveSnapshot(t.Context(), updated); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(name)
	if err != nil || string(data) != "updated\n"+original {
		t.Fatal("cancelled page retargeted an edit", err)
	}
}
