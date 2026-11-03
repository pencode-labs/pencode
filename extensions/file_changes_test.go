package extensions

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/pencode-labs/pencode/peb"
)

func TestFileChangesReachActionAndEditorSDKHandlers(t *testing.T) {
	backend, _ := actionFixture(t)
	bundle, err := Load(context.Background(), backend, "test")
	if err != nil {
		t.Fatal(err)
	}
	batch := peb.FileChanges{Root: t.TempDir(), Files: []peb.FileEvent{{Path: "文件.go", Op: peb.FileWritten}}}
	if _, err := backend.OpenWorkspace(t.Context(), batch.Root); err != nil {
		t.Fatal(err)
	}
	want := batch.Root + ":" + batch.Files[0].Path
	actions := bundle.Actions.Start(nil, nil)
	defer actions.Close()
	actions.NotifyFiles(batch)
	timer := time.After(5 * time.Second)
	for {
		select {
		case result, ok := <-actions.Results:
			if !ok || result.Err != nil {
				t.Fatalf("action session: %+v", result)
			}
			if result.Change.Action.Text == want {
				goto editor
			}
		case <-timer:
			t.Fatal("file notification never reached action handler")
		}
	}
editor:
	definition := actions.Editor(peb.Action{ID: "test/run"})
	session := definition.Start(batch.Root, nil, nil)
	defer session.Close()
	session.NotifyFiles(batch)
	// Observe the next editor reply only after the file notification was handled.
	for {
		if !session.Submit(peb.EditorMessage{Path: "probe.go"}) {
			t.Fatal("editor request rejected")
		}
		select {
		case result := <-session.Results:
			if result.Err != nil {
				t.Fatal(result.Err)
			}
			if result.Reply.Content != nil && *result.Reply.Content == want {
				return
			}
		case <-timer:
			t.Fatal("file notification never reached editor handler")
		}
	}
}

func TestFileChangesMailboxCoalescesAndSignalsReset(t *testing.T) {
	var mailbox fileChangeMailbox
	mailbox.initialize()
	batch := peb.FileChanges{Root: "/project", Files: []peb.FileEvent{{Path: "main.go", Op: peb.FileCreated}}}
	mailbox.submit(batch)
	batch.Files[0].Op = peb.FileWritten
	if n := testing.AllocsPerRun(100, func() { mailbox.submit(batch) }); n != 0 {
		t.Fatalf("warm file notification allocated %g", n)
	}
	<-mailbox.ready
	got := mailbox.take()
	if got.Reset || len(got.Files) != 1 || got.Files[0].Op != peb.FileCreated|peb.FileWritten {
		t.Fatalf("coalesced files: %+v", got)
	}
	for i := range fileChangeCapacity + 1 {
		batch.Files[0].Path = fmt.Sprint(i)
		mailbox.submit(batch)
	}
	<-mailbox.ready
	got = mailbox.take()
	if !got.Reset || len(got.Files) != fileChangeCapacity {
		t.Fatal("slow extension silently lost events")
	}
	if len(mailbox.roots) != 0 {
		t.Fatal("mailbox retained a historical workspace")
	}
	mailbox.stop()
	mailbox.submit(batch)
	if len(mailbox.roots) != 0 {
		t.Fatal("closed session retained events")
	}
}
