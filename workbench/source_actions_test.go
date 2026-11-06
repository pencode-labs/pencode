package workbench

import (
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSourceControlStageFolder(t *testing.T) {
	for _, dirty := range []bool{false, true} {
		name := "saved"
		if dirty {
			name = "unsaved"
		}
		t.Run(name, func(t *testing.T) {
			r := sourceTestRepository(t)
			const folder = "resources/目录 #1-all"
			modified, deleted := folder+"/main.go", folder+"/nested/deleted.go"
			staged, added := folder+"/staged.go", folder+"/nested/new.go"
			sibling, outside := folder+"-other/keep.go", "outside.go"
			for _, file := range []string{modified, deleted, staged, sibling, outside} {
				sourceTestWrite(t, r, file, "base\n")
			}
			sourceTestGit(t, r, "add", "--all")
			sourceTestGit(t, r, "commit", "-m", "base")
			for _, file := range []string{modified, staged, sibling, outside, added} {
				sourceTestWrite(t, r, file, "changed\n")
			}
			sourceTestGit(t, r, "add", "--", staged)
			if err := os.Remove(filepath.Join(r.Root, filepath.FromSlash(deleted))); err != nil {
				t.Fatal(err)
			}
			project := &pencodeProject{Path: r.Root, Files: map[string]*openFile{
				staged: {Dirty: true}, // Staged-only drafts do not belong to this batch.
			}}
			if dirty {
				project.Files[added] = &openFile{Dirty: true}
			}
			m := &workspaceModel{projects: []*pencodeProject{project}, activeProjectIndex: 0}
			s := m.sourceState()
			t.Cleanup(s.stop)
			s.Snapshot = sourceTestSnapshot(t, r)
			s.Collapsed = map[string]bool{"changes:" + folder: true}
			var view sourceVNodeView
			view.reset()
			m.sourceFileTree(&view, "changes", "Changes", s.Snapshot.Groups[2], 14)
			for _, id := range []string{"sc-stage-folder:" + url.PathEscape(folder), "sc-discard-folder:changes:" + url.PathEscape(folder)} {
				if !slices.Contains(view.clicks, id) {
					t.Fatalf("collapsed folder is missing action %q", id)
				}
			}
			m.sourceAction("sc-stage-folder:" + url.PathEscape(folder))
			if dirty {
				if s.Done != nil || s.Notice == "" {
					t.Fatal("unsaved descendant did not block the entire batch")
				}
			} else {
				if s.Done == nil {
					t.Fatal("folder action did not start staging")
				}
				select {
				case result := <-s.Done:
					if result.Err != nil {
						t.Fatal(result.Err)
					}
				case <-time.After(10 * time.Second):
					t.Fatal("folder staging timed out")
				}
			}
			snapshot := sourceTestSnapshot(t, r)
			for _, file := range snapshot.Files {
				wantStaged := file.Path == staged || !dirty && strings.HasPrefix(file.Path, folder+"/")
				if got := file.Index != ' ' && file.Index != '?'; got != wantStaged {
					t.Fatalf("file %q staged=%v, want %v", file.Path, got, wantStaged)
				}
			}
			wantCount := 4
			if dirty {
				wantCount = 1
			}
			if len(snapshot.Groups[1]) != wantCount {
				t.Fatalf("unexpected staged files: %+v", snapshot.Groups[1])
			}
		})
	}
}

func TestSourceControlStageAndUnstageAll(t *testing.T) {
	for _, hasHead := range []bool{false, true} {
		t.Run(strconv.FormatBool(hasHead), func(t *testing.T) {
			r := sourceTestRepository(t)
			if hasHead {
				sourceTestWrite(t, r, "tracked.go", "base\n")
				sourceTestGit(t, r, "add", "--all")
				sourceTestGit(t, r, "commit", "-m", "base")
			}
			for _, name := range []string{"tracked.go", "nested/new.go"} {
				sourceTestWrite(t, r, name, "changed\n")
			}
			m := &workspaceModel{}
			s := m.sourceState()
			t.Cleanup(s.stop)
			s.Snapshot = sourceTestSnapshot(t, r)
			for _, action := range []string{"sc-stage-all", "sc-unstage-all"} {
				m.sourceAction(action)
				if s.Done == nil {
					t.Fatalf("%s did not start an operation", action)
				}
				select {
				case result := <-s.Done:
					if result.Err != nil {
						t.Fatal(result.Err)
					}
					s.Snapshot, s.Done = *result.Snapshot, nil
				case <-time.After(10 * time.Second):
					t.Fatal("bulk operation timed out")
				}
				group := 1
				if action == "sc-unstage-all" {
					group = 2
				}
				if len(s.Snapshot.Groups[group]) != 2 || len(s.Snapshot.Groups[3-group]) != 0 {
					t.Fatalf("%s produced unexpected groups: %+v", action, s.Snapshot.Groups)
				}
			}
		})
	}
}

func TestSourceControlDiscardAll(t *testing.T) {
	for _, dirty := range []bool{false, true} {
		t.Run(strconv.FormatBool(dirty), func(t *testing.T) {
			r := sourceTestRepository(t)
			for _, name := range []string{"modified.go", "deleted.go", "staged.go"} {
				sourceTestWrite(t, r, name, "base\n")
			}
			sourceTestGit(t, r, "add", "--all")
			sourceTestGit(t, r, "commit", "-m", "base")
			for _, name := range []string{"modified.go", "staged.go"} {
				sourceTestWrite(t, r, name, "staged\n")
			}
			sourceTestGit(t, r, "add", "--all")
			sourceTestWrite(t, r, "modified.go", "unstaged\n")
			sourceTestWrite(t, r, "nested/new.go", "untracked\n")
			if err := os.Remove(filepath.Join(r.Root, "deleted.go")); err != nil {
				t.Fatal(err)
			}
			project := &pencodeProject{Path: r.Root, Files: map[string]*openFile{
				"nested/new.go": {Dirty: dirty},
			}}
			m := &workspaceModel{projects: []*pencodeProject{project}, activeProjectIndex: 0}
			s := m.sourceState()
			t.Cleanup(s.stop)
			s.Snapshot = sourceTestSnapshot(t, r)
			m.sourceAction("sc-discard-all")
			if s.Prompt != "discard-all" || s.Done != nil {
				t.Fatal("bulk discard did not wait for confirmation")
			}
			m.sourceAction("sc-prompt-cancel")
			if len(sourceTestSnapshot(t, r).Groups[2]) != 3 {
				t.Fatal("cancelling bulk discard changed the worktree")
			}
			m.sourceAction("sc-discard-all")
			m.sourceAction("sc-prompt-confirm")
			if dirty {
				if s.Notice == "" || s.Done != nil || len(sourceTestSnapshot(t, r).Groups[2]) != 3 {
					t.Fatal("unsaved file did not block the entire discard batch")
				}
				return
			}
			if s.Done == nil {
				t.Fatal("confirmation did not start bulk discard")
			}
			select {
			case result := <-s.Done:
				if result.Err != nil {
					t.Fatal(result.Err)
				}
				if len(result.Snapshot.Groups[2]) != 0 || len(result.Snapshot.Groups[1]) != 2 {
					t.Fatalf("discard failed to preserve the index: %+v", result.Snapshot.Groups)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("bulk discard timed out")
			}
			if got := sourceTestGit(t, r, "diff", "--cached", "--", "modified.go"); !strings.Contains(got, "+staged") {
				t.Fatalf("discard changed staged content: %s", got)
			}
		})
	}
}
