package extensions

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pencode-labs/pencode/peb"
)

func TestSharedRuntimeHelperProcess(t *testing.T) {
	if os.Args[len(os.Args)-1] != "peb-shared-helper" {
		return
	}
	action := peb.Action{ID: "run", Text: "Run", Location: peb.StatusBar}
	var publisher *peb.ActionPublisher
	err := peb.ServeExtension(os.Stdin, os.Stdout, peb.Contributions{Actions: []peb.Action{
		action, {ID: "view", Text: "View", Location: peb.OpenWith, Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".txt"}}},
	}}, peb.Handlers{
		ActivateActions: func(_ context.Context, p *peb.ActionPublisher) {
			publisher = p
			action.Text = strconv.Itoa(os.Getpid())
			_ = p.Update(action)
		},
		Action: func(peb.ActionContext) error { return publisher.Update(action) },
		NewEditor: func(ctx context.Context) func(peb.EditorMessage) (peb.EditorReply, error) {
			count := 0
			return func(message peb.EditorMessage) (peb.EditorReply, error) {
				if message.Event.Kind == "crash" {
					os.Exit(3)
				}
				count++
				value := fmt.Sprintf("%d:%d", os.Getpid(), count)
				return peb.EditorReply{Content: &value}, ctx.Err()
			}
		},
	})
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestWindowRuntimeSharesProcessAndKeepsViewsIndependent(t *testing.T) {
	backend, directory := actionFixture(t)
	changeFixtureManifest(t, filepath.Join(directory, "test"), func(m *Manifest) {
		m.Args = []string{"-test.run=^TestSharedRuntimeHelperProcess$", "--", "peb-shared-helper"}
	})
	bundle, err := Load(t.Context(), backend, "test")
	if err != nil {
		t.Fatal(err)
	}
	actions := bundle.Actions.Start(nil, nil)
	defer actions.Close()
	nextAction := func() int {
		t.Helper()
		select {
		case result := <-actions.Results:
			if result.Err != nil {
				t.Fatal(result.Err)
			}
			pid, err := strconv.Atoi(result.Change.Action.Text)
			if err != nil {
				t.Fatal(result)
			}
			return pid
		case <-time.After(5 * time.Second):
			t.Fatal("action timed out")
		}
		return 0
	}
	pid := nextAction()
	reply := func(s *EditorSession, want int) int {
		t.Helper()
		if !s.Submit(peb.EditorMessage{Editor: "view", Path: "same.txt"}) {
			t.Fatal("view rejected request")
		}
		select {
		case result := <-s.Results:
			if result.Err != nil || result.Reply.Content == nil {
				t.Fatal(result)
			}
			parts := strings.Split(*result.Reply.Content, ":")
			if len(parts) != 2 || parts[1] != strconv.Itoa(want) {
				t.Fatal("view state leaked", *result.Reply.Content)
			}
			id, err := strconv.Atoi(parts[0])
			if err != nil {
				t.Fatal(err)
			}
			return id
		case <-time.After(5 * time.Second):
			t.Fatal("view timed out")
		}
		return 0
	}
	// More views than the old per-process stream budget, all for the same path.
	views := make([]*EditorSession, 12)
	for i := range views {
		views[i] = bundle.Editors[0].Start("", nil, nil)
		defer views[i].Close()
		if reply(views[i], 1) != pid {
			t.Fatal("view started another process")
		}
	}
	views[0].Close()
	select {
	case <-views[0].Done:
	case <-time.After(5 * time.Second):
		t.Fatal("view did not close")
	}
	if reply(views[1], 2) != pid {
		t.Fatal("closing a view restarted the process")
	}
	if !actions.Submit(peb.ActionContext{ID: "extensions/test/run"}) {
		t.Fatal("action did not survive view close")
	}
	if nextAction() != pid {
		t.Fatal("action process changed")
	}
	for _, view := range views {
		view.Close()
		select {
		case <-view.Done:
		case <-time.After(5 * time.Second):
			t.Fatal("view did not close")
		}
	}
	actions.Close()
	select {
	case <-actions.Done:
	case <-time.After(5 * time.Second):
		t.Fatal("action did not close")
	}
	reopened := bundle.Editors[0].Start("", nil, nil)
	defer reopened.Close()
	if reply(reopened, 1) != pid {
		t.Fatal("idle runtime was not retained")
	}
	other := extensionBackend(t, directory)
	second, err := Load(t.Context(), other, "test")
	if err != nil {
		t.Fatal(err)
	}
	isolated := second.Editors[0].Start("", nil, nil)
	defer isolated.Close()
	if reply(isolated, 1) == pid {
		t.Fatal("different windows shared a process")
	}
	// A process crash disconnects every attached view; a new view can restart it.
	survivor := bundle.Editors[0].Start("", nil, nil)
	defer survivor.Close()
	if reply(survivor, 1) != pid {
		t.Fatal("runtime changed")
	}
	if !reopened.Submit(peb.EditorMessage{Editor: "view", Event: peb.EditorEvent{Kind: "crash"}}) {
		t.Fatal("crash request rejected")
	}
	for _, view := range []*EditorSession{reopened, survivor} {
		select {
		case <-view.Done:
		case <-time.After(5 * time.Second):
			t.Fatal("crash did not disconnect a view")
		}
	}
	restarted := bundle.Editors[0].Start("", nil, nil)
	defer restarted.Close()
	if reply(restarted, 1) == pid {
		t.Fatal("crashed runtime was reused")
	}
	if _, _, err := SetEnabled(t.Context(), backend, "test", false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-restarted.Done:
	case <-time.After(5 * time.Second):
		t.Fatal("disabled extension retained a view")
	}
}
