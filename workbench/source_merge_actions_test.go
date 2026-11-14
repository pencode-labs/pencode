package workbench

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/onix-engine/onix/input"
)

func sourceTestConflict(t *testing.T) (sourceRepository, string) {
	t.Helper()
	r := sourceTestRepository(t)
	name := "resources/conflict.txt"
	sourceTestWrite(t, r, name, "base\n")
	sourceTestGit(t, r, "add", "--all")
	sourceTestGit(t, r, "commit", "-m", "base")
	sourceTestGit(t, r, "switch", "-c", "incoming")
	sourceTestWrite(t, r, name, "incoming\n")
	sourceTestGit(t, r, "commit", "-am", "incoming")
	sourceTestGit(t, r, "switch", "main")
	sourceTestWrite(t, r, name, "current\n")
	sourceTestGit(t, r, "commit", "-am", "current")
	return r, name
}

func TestSourceControlMergeConflictFeedback(t *testing.T) {
	r, _ := sourceTestConflict(t)
	m := &workspaceModel{projectPath: r.Root, backend: r.Backend}
	s := m.sourceState()
	s.Snapshot = sourceTestSnapshot(t, r)
	s.Prompt, s.PromptHash = "merge", "incoming"
	m.confirmSourceOperation()
	result := <-s.Done
	if result.Err == nil {
		t.Fatal("expected conflict")
	}
	s.Done <- result
	m.pollSourceResult(s, nil)
	defer s.stop()
	if s.Notice != "" || s.CommitMessage != "Merge from incoming" || len(s.Snapshot.Groups[0]) != 1 {
		t.Fatalf("notice=%q message=%q files=%v", s.Notice, s.CommitMessage, s.Snapshot.Files)
	}
	s.CommitMessage = "custom merge message"
	s.Done = make(chan sourceResult, 1)
	s.Done <- sourceResult{Snapshot: &s.Snapshot}
	m.pollSourceResult(s, nil)
	if s.CommitMessage != "custom merge message" {
		t.Fatal("refresh overwrote draft")
	}
	// Resolving/staging errors must still be shown even during a merge.
	result.Message = "Merge resolved and staged"
	if result.expectedGitError() {
		t.Fatal("save error was suppressed")
	}
}

func TestSourceControlMergeContextMenu(t *testing.T) {
	for _, side := range []string{"local", "incoming"} {
		t.Run(side, func(t *testing.T) {
			r, name := sourceTestConflict(t)
			if _, err := r.command("merge", "incoming"); err == nil {
				t.Fatal("expected conflict")
			}
			t.Setenv("ONIX_PENCODE_PROFILE_PROJECT", r.Root)
			t.Setenv("ONIX_PENCODE_PROFILE_FILE", name)
			app, m, _, state := languageInteractionFixture(t)
			editorContextClick(t, app, m, state, "status-source-control")
			sourceWait(t, app, m)
			s := m.sourceState()
			bounds := editorContextBounds(t, m, "sc-open:merge:"+url.PathEscape(name))
			zoom := float64(m.settings.Zoom) / 100
			x, y := float64(bounds.X+10)*zoom, float64(bounds.Y+10)*zoom
			state.Enqueue(input.Event{Kind: input.EventMouseMove, X: x, Y: y})
			state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseRight, X: x, Y: y})
			state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseRight, X: x, Y: y})
			languageInteractionStep(t, app)
			if !s.MenuOpen || s.MenuPath != name {
				t.Fatal("merge menu did not open")
			}
			editorContextClick(t, app, m, state, "sc-use:"+side)
			sourceWait(t, app, m)
			want := "current\n"
			if side == "incoming" {
				want = "incoming\n"
			}
			data, err := r.workFile(name)
			if err != nil || string(data) != want || len(s.Snapshot.Groups[0]) != 0 {
				t.Fatalf("result=%q err=%v conflicts=%v", data, err, s.Snapshot.Groups[0])
			}
			if got := sourceTestGit(t, r, "show", ":"+name); got+"\n" != want {
				t.Fatalf("index=%q", got)
			}
			if s.CommitMessage != "Merge from incoming" {
				t.Fatal(s.CommitMessage)
			}
			if s.Snapshot.syncPending() {
				t.Fatal("unfinished merge offered synchronization")
			}
			editorContextClick(t, app, m, state, "sc-create-commit")
			sourceWait(t, app, m)
			if s.CommitMessage != "" || len(strings.Fields(sourceTestGit(t, r, "show", "-s", "--format=%P", "HEAD"))) != 2 {
				t.Fatal("resolved merge was not committed")
			}
		})
	}
}

func TestSourceControlPushRejectionFeedback(t *testing.T) {
	for _, message := range []string{"git push: [rejected] main -> main (fetch first)", "git push: [rejected] main -> main (non-fast-forward)", "git push: authentication failed"} {
		m := &workspaceModel{}
		s := m.sourceState()
		s.Done = make(chan sourceResult, 1)
		s.Done <- sourceResult{Err: errors.New(message), Message: "Git push completed"}
		m.pollSourceResult(s, nil)
		if (s.Notice == "") != sourcePushRejected(errors.New(message)) {
			t.Fatalf("unexpected notice for %q: %q", message, s.Notice)
		}
	}
}

func TestSourceControlPushRefreshesRemote(t *testing.T) {
	remote := sourceRepository{Root: t.TempDir(), Backend: newPencodeBackend(t)}
	opened, err := remote.Backend.OpenWorkspace(t.Context(), remote.Root)
	if err != nil {
		t.Fatal(err)
	}
	remote.Root = opened.Path
	sourceTestGit(t, remote, "init", "--bare", "-b", "main")
	local := sourceTestRepository(t)
	sourceTestGit(t, local, "commit", "--allow-empty", "-m", "base")
	sourceTestGit(t, local, "remote", "add", "origin", remote.Root)
	sourceTestGit(t, local, "push", "--set-upstream", "origin", "main")
	peer := sourceTestRepository(t)
	sourceTestGit(t, peer, "remote", "add", "origin", remote.Root)
	sourceTestGit(t, peer, "fetch")
	sourceTestGit(t, peer, "reset", "--hard", "origin/main")
	sourceTestGit(t, peer, "commit", "--allow-empty", "-m", "remote")
	sourceTestGit(t, peer, "push", "origin", "main")
	sourceTestGit(t, local, "commit", "--allow-empty", "-m", "local")
	m := &workspaceModel{projectPath: local.Root, backend: local.Backend}
	s := m.sourceState()
	s.Snapshot = sourceTestSnapshot(t, local)
	s.Prompt = "push"
	m.confirmSourceOperation()
	result := <-s.Done
	if !sourcePushRejected(result.Err) {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	s.Done <- result
	m.pollSourceResult(s, nil)
	defer s.stop()
	if s.Notice != "" || s.Snapshot.Sync != (sourceSyncStatus{Ahead: 1, Behind: 1}) || !s.Snapshot.syncPending() {
		t.Fatalf("notice=%q sync=%+v", s.Notice, s.Snapshot.Sync)
	}
}
