package workbench

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func sourceTestRepository(t testing.TB) sourceRepository {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git unavailable")
	}
	r := sourceRepository{Root: t.TempDir(), Backend: newPencodeBackend(t)}
	opened, err := r.Backend.OpenWorkspace(t.Context(), r.Root)
	if err != nil {
		t.Fatal(err)
	}
	r.Root = opened.Path
	sourceTestGit(t, r, "init", "-b", "main")
	for key, value := range map[string]string{"user.name": "Onix Test", "user.email": "onix@example.invalid", "commit.gpgsign": "false", "core.autocrlf": "false", "core.hooksPath": filepath.Join(r.Root, "no-hooks")} {
		sourceTestGit(t, r, "config", key, value)
	}
	return r
}

func sourceTestGit(t testing.TB, r sourceRepository, args ...string) string {
	t.Helper()
	data, err := r.command(args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

func sourceTestWrite(t testing.TB, r sourceRepository, name, value string) {
	t.Helper()
	full := filepath.Join(r.Root, name)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(value), 0644); err != nil {
		t.Fatal(err)
	}
}

func sourceTestSnapshot(t testing.TB, r sourceRepository) sourceSnapshot {
	t.Helper()
	s, err := r.snapshot(200)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSourceControlHistoryPagination(t *testing.T) {
	r := sourceTestRepository(t)
	sourceTestGit(t, r, "commit", "--allow-empty", "-m", "base")
	sourceTestGit(t, r, "branch", "topic")
	for range 12 {
		sourceTestGit(t, r, "commit", "--allow-empty", "-m", "main")
	}
	sourceTestGit(t, r, "switch", "topic")
	for range 10 {
		sourceTestGit(t, r, "commit", "--allow-empty", "-m", "topic")
	}
	sourceTestGit(t, r, "switch", "main")
	sourceTestGit(t, r, "merge", "--no-ff", "topic", "-m", "merge")
	full := sourceTestSnapshot(t, r)
	page, err := r.historyPage(full.HistoryRefs, 0, 7, sourceGraph{})
	if err != nil || !page.HasMore || len(page.Commits) != 7 {
		t.Fatalf("first page: %+v, %v", page, err)
	}
	commits := append([]sourceCommit(nil), page.Commits...)
	// A new tip between page requests must not shift or duplicate history.
	sourceTestGit(t, r, "commit", "--allow-empty", "-m", "new tip")
	for page.HasMore {
		before := slices.Clone(page.Graph.lanes)
		beforeColor := page.Graph.nextColor
		next, err := r.historyPage(full.HistoryRefs, len(commits), 7, page.Graph)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(before, page.Graph.lanes) || beforeColor != page.Graph.nextColor {
			t.Fatal("loading a page mutated the foreground graph")
		}
		commits = append(commits, next.Commits...)
		page = next
	}
	if !reflect.DeepEqual(commits, full.Commits) {
		t.Fatal("paged commits or branch edges differ from a single traversal")
	}
	updated := sourceTestSnapshot(t, r)
	if updated.HistoryRefs == full.HistoryRefs || len(updated.Commits) != len(full.Commits)+1 {
		t.Fatal("new snapshot did not pick up the new tip")
	}
}

func TestSourceControlStatusPaths(t *testing.T) {
	files, err := parseSourceStatus([]byte(" M folder/space name.go\x00R  new\nname.go\x00old name.go\x00UU conflict.go\x00?? :(glob)*\x00"))
	if err != nil || len(files) != 4 {
		t.Fatal(files, err)
	}
	var rename, conflict bool
	for _, file := range files {
		if file.Path == "new\nname.go" {
			rename = file.Previous == "old name.go"
		}
		if file.Path == "conflict.go" {
			conflict = file.Conflict
		}
	}
	if !rename || !conflict {
		t.Fatal(files)
	}
	if _, err := parseSourceStatus([]byte("R  new\x00")); err == nil {
		t.Fatal("truncated rename accepted")
	}
}

func TestSourceControlStageCommitUnstageAndHistory(t *testing.T) {
	r := sourceTestRepository(t)
	name := "resources/space name.txt"
	sourceTestWrite(t, r, name, "first\nsecond\n")
	s := sourceTestSnapshot(t, r)
	if s.HasHead || len(s.Files) != 1 || s.Branch != "main" {
		t.Fatal(s)
	}
	if err := r.stage(s.Files); err != nil {
		t.Fatal(err)
	}
	s = sourceTestSnapshot(t, r)
	if s.Files[0].Index != 'A' {
		t.Fatal(s.Files)
	}
	if err := r.unstage(s.Files, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.workFile(name); string(got) != "first\nsecond\n" {
		t.Fatal("unstage deleted working file")
	}
	if err := r.stage(sourceTestSnapshot(t, r).Files); err != nil {
		t.Fatal(err)
	}
	if err := r.commit("initial"); err != nil {
		t.Fatal(err)
	}
	s = sourceTestSnapshot(t, r)
	if !s.HasHead || len(s.Commits) != 1 || len(s.Files) != 0 {
		t.Fatal(s)
	}
	files, err := r.commitFiles(s.Commits[0].Hash)
	if err != nil || len(files) != 1 || files[0].Index != 'A' {
		t.Fatal(files, err)
	}
	tab, err := r.openDiff(files[0], "history", s.Commits[0].Hash)
	if err != nil || len(tab.Rows) != 2 || tab.Rows[0].RightKind != sourceAdded || tab.Rows[0].LeftNumber != 0 {
		t.Fatal(tab, err)
	}
	sourceTestWrite(t, r, name, "first\nstaged\n")
	if err := r.stage(sourceTestSnapshot(t, r).Files); err != nil {
		t.Fatal(err)
	}
	sourceTestWrite(t, r, name, "first\nworking\n")
	s = sourceTestSnapshot(t, r)
	if s.Files[0].Index != 'M' || s.Files[0].Work != 'M' {
		t.Fatal(s.Files)
	}
	for kind, want := range map[string]string{"staged": "staged", "changes": "working"} {
		tab, err := r.openDiff(s.Files[0], kind, "")
		if err != nil || len(tab.Rows) != 2 || tab.Rows[1].Right != want || tab.Rows[1].RightKind != sourceAdded {
			t.Fatal(kind, tab, err)
		}
	}
	if err := r.unstage(s.Files, true); err != nil {
		t.Fatal(err)
	}
	if s := sourceTestSnapshot(t, r); s.Files[0].Index != ' ' || s.Files[0].Work != 'M' {
		t.Fatal(s.Files)
	}
	if err := r.commit("  "); err == nil {
		t.Fatal("empty commit message accepted")
	}
}

func TestSourceControlDiscardRestoresWorktreeAndRemovesUntracked(t *testing.T) {
	r := sourceTestRepository(t)
	sourceTestWrite(t, r, "modified.txt", "base\n")
	sourceTestWrite(t, r, "removed.txt", "kept\n")
	sourceTestGit(t, r, "add", "--all")
	sourceTestGit(t, r, "commit", "-m", "base")
	sourceTestWrite(t, r, "modified.txt", "changed\n")
	if err := os.Remove(filepath.Join(r.Root, "removed.txt")); err != nil {
		t.Fatal(err)
	}
	sourceTestWrite(t, r, "untracked.txt", "new\n")
	if err := r.discardChanges(sourceTestSnapshot(t, r).Files); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.workFile("modified.txt"); string(got) != "base\n" {
		t.Fatalf("modified file=%q", got)
	}
	if got, _ := r.workFile("removed.txt"); string(got) != "kept\n" {
		t.Fatalf("removed file=%q", got)
	}
	if got, _ := r.workFile("untracked.txt"); got != nil {
		t.Fatalf("untracked file survived discard: %q", got)
	}
	if s := sourceTestSnapshot(t, r); len(s.Files) != 0 {
		t.Fatalf("discard left changes: %+v", s.Files)
	}
}

func TestSourceControlSmartCommit(t *testing.T) {
	for _, mode := range []string{"changes", "staged", "mixed", "partial", "initial", "empty-message"} {
		t.Run(mode, func(t *testing.T) {
			r := sourceTestRepository(t)
			if mode != "initial" {
				sourceTestWrite(t, r, "resources/tracked.txt", "base\n")
				sourceTestGit(t, r, "add", "--all")
				sourceTestGit(t, r, "commit", "-m", "base")
			}
			sourceTestWrite(t, r, "resources/tracked.txt", "staged\n")
			if mode == "staged" || mode == "mixed" || mode == "partial" {
				sourceTestGit(t, r, "add", "--all")
			}
			if mode == "partial" {
				sourceTestWrite(t, r, "resources/tracked.txt", "unstaged\n")
			}
			if mode != "staged" {
				sourceTestWrite(t, r, "resources/new.txt", "untracked\n")
			}
			message := "subject\n\nbody"
			if mode == "empty-message" {
				message = " \n "
			}
			err := r.commit(message)
			if mode == "empty-message" {
				if err == nil || sourceTestGit(t, r, "diff", "--cached", "--name-only") != "" {
					t.Fatal("empty message must fail before staging")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := sourceTestGit(t, r, "show", "HEAD:resources/tracked.txt"); got != "staged" {
				t.Fatalf("committed content=%q", got)
			}
			s := sourceTestSnapshot(t, r)
			switch mode {
			case "mixed":
				if len(s.Files) != 1 || s.Files[0].Path != "resources/new.txt" || s.Files[0].Index != '?' {
					t.Fatalf("unstaged file was committed: %+v", s.Files)
				}
			case "partial":
				if len(s.Files) != 2 || len(s.Groups[1]) != 0 || sourceTestGit(t, r, "diff", "--", "resources/tracked.txt") == "" {
					t.Fatal("partial staging did not preserve working tree changes")
				}
			default:
				if len(s.Files) != 0 {
					t.Fatalf("changes not committed: %+v", s.Files)
				}
			}
		})
	}
}

func TestSourceControlSyncChanges(t *testing.T) {
	for _, mode := range []string{"push", "pull", "both", "current", "dirty", "conflict"} {
		t.Run(mode, func(t *testing.T) {
			remote := sourceRepository{Root: t.TempDir(), Backend: newPencodeBackend(t)}
			opened, openErr := remote.Backend.OpenWorkspace(t.Context(), remote.Root)
			if openErr != nil {
				t.Fatal(openErr)
			}
			remote.Root = opened.Path
			sourceTestGit(t, remote, "init", "--bare", "-b", "main")
			local := sourceTestRepository(t)
			sourceTestWrite(t, local, "resources/shared.txt", "base\n")
			sourceTestGit(t, local, "add", "--all")
			sourceTestGit(t, local, "commit", "-m", "base")
			if s := sourceTestSnapshot(t, local); s.Sync != (sourceSyncStatus{}) || s.syncPending() {
				t.Fatal("repository without an upstream offered synchronization")
			}
			sourceTestGit(t, local, "remote", "add", "origin", remote.Root)
			sourceTestGit(t, local, "push", "--set-upstream", "origin", "main")
			peer := sourceTestRepository(t)
			sourceTestGit(t, peer, "remote", "add", "origin", remote.Root)
			sourceTestGit(t, peer, "fetch")
			sourceTestGit(t, peer, "reset", "--hard", "origin/main")
			sourceTestGit(t, peer, "branch", "--set-upstream-to=origin/main", "main")
			want := sourceSyncStatus{}
			if mode == "push" || mode == "both" || mode == "conflict" {
				name := "resources/local.txt"
				if mode == "conflict" {
					name = "resources/shared.txt"
				}
				sourceTestWrite(t, local, name, "local\n")
				if err := local.commit("local"); err != nil {
					t.Fatal(err)
				}
				want.Ahead = 1
			}
			if mode == "pull" || mode == "both" || mode == "conflict" {
				name := "resources/remote.txt"
				if mode == "conflict" {
					name = "resources/shared.txt"
				}
				sourceTestWrite(t, peer, name, "remote\n")
				if err := peer.commit("remote"); err != nil {
					t.Fatal(err)
				}
				sourceTestGit(t, peer, "push")
				want.Behind = 1
			}
			sourceTestGit(t, local, "fetch")
			snapshot := sourceTestSnapshot(t, local)
			if snapshot.Sync != want || snapshot.syncPending() != (want.Ahead+want.Behind > 0) {
				t.Fatalf("synchronization counts=%+v, want %+v", snapshot.Sync, want)
			}
			remoteHead := sourceTestGit(t, remote, "rev-parse", "HEAD")
			if mode == "dirty" {
				sourceTestWrite(t, local, "resources/shared.txt", "dirty\n")
			}
			var err error
			if mode == "push" {
				m := &workspaceModel{projectPath: local.Root, backend: local.Backend}
				s := m.sourceState()
				s.Snapshot, s.CommitMessage = snapshot, "keep this draft"
				m.sourceAction("sc-create-commit")
				if s.Done == nil || s.JobKind != sourceSync {
					t.Fatal("primary button did not start synchronization")
				}
				result := <-s.Done
				err = result.Err
				if !result.Reload || result.Message != "Changes synchronized" {
					t.Fatal("synchronization did not request a workspace refresh")
				}
				s.Done <- result
				m.pollSourceResult(s, nil)
				s.stop()
				if s.CommitMessage != "keep this draft" || s.Snapshot.syncPending() {
					t.Fatal("synchronization lost the draft message or retained stale counts")
				}
			} else {
				err = local.syncChanges()
			}
			after := sourceTestSnapshot(t, local)
			if mode == "conflict" || mode == "dirty" {
				if err == nil || sourceTestGit(t, remote, "rev-parse", "HEAD") != remoteHead {
					t.Fatal("failed synchronization changed remote history")
				}
				if mode == "conflict" && len(after.Groups[0]) != 1 {
					t.Fatal("pull conflict was not retained for the merge editor")
				}
				return
			}
			if err != nil || after.Sync != (sourceSyncStatus{}) || len(after.Files) != 0 {
				t.Fatalf("synchronization failed: %v, counts=%+v files=%+v", err, after.Sync, after.Files)
			}
			if head := sourceTestGit(t, local, "rev-parse", "HEAD"); head != sourceTestGit(t, remote, "rev-parse", "HEAD") {
				t.Fatal("local and remote heads differ after synchronization")
			}
		})
	}
}

func TestSourceControlRenameDeleteAndLiteralPaths(t *testing.T) {
	r := sourceTestRepository(t)
	for _, name := range []string{"old.txt", "deleted.txt", "one.txt", "two.txt"} {
		sourceTestWrite(t, r, name, name+"\n")
	}
	sourceTestGit(t, r, "add", "--all")
	sourceTestGit(t, r, "commit", "-m", "initial")
	sourceTestGit(t, r, "mv", "old.txt", "new.txt")
	if err := os.Remove(filepath.Join(r.Root, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	magic := ":(glob)*"
	sourceTestWrite(t, r, magic, "literal\n")
	if err := r.stage([]sourceFile{{Path: magic}}); err != nil {
		t.Fatal(err)
	}
	s := sourceTestSnapshot(t, r)
	var renamed sourceFile
	for _, file := range s.Files {
		if file.Path == "new.txt" {
			renamed = file
		}
		if file.Path == "one.txt" || file.Path == "two.txt" {
			t.Fatal("pathspec expanded", file)
		}
	}
	if renamed.Previous != "old.txt" {
		t.Fatal(s.Files)
	}
	tab, err := r.openDiff(renamed, "staged", "")
	if err != nil || len(tab.Rows) != 1 || tab.Rows[0].Left != tab.Rows[0].Right {
		t.Fatal(tab, err)
	}
	if err := r.stage(s.Files); err != nil {
		t.Fatal(err)
	}
	if err := r.commit("rename and delete"); err != nil {
		t.Fatal(err)
	}
	hash := sourceTestGit(t, r, "rev-parse", "HEAD")
	files, err := r.commitFiles(hash)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(files, func(f sourceFile) bool { return f.Path == "new.txt" && f.Previous == "old.txt" }) {
		t.Fatal(files)
	}
	for _, file := range files {
		if file.Path == "deleted.txt" {
			tab, err := r.openDiff(file, "history", hash)
			if err != nil || len(tab.Rows) != 1 || tab.Rows[0].LeftKind != sourceDeleted || tab.Rows[0].RightNumber != 0 {
				t.Fatal(tab, err)
			}
		}
	}
}

func TestSourceControlMergeWorkflow(t *testing.T) {
	r := sourceTestRepository(t)
	name := "resources/conflict.txt"
	sourceTestWrite(t, r, name, "before\nbase\nafter\n")
	sourceTestGit(t, r, "add", "--all")
	sourceTestGit(t, r, "commit", "-m", "base")
	sourceTestGit(t, r, "switch", "-c", "incoming")
	sourceTestWrite(t, r, name, "before\nincoming\nafter\n")
	sourceTestGit(t, r, "commit", "-am", "incoming")
	sourceTestGit(t, r, "switch", "main")
	sourceTestWrite(t, r, name, "before\nlocal\nafter\n")
	sourceTestGit(t, r, "commit", "-am", "local")
	if _, err := r.command("merge", "incoming"); err == nil {
		t.Fatal("expected conflict")
	}
	s := sourceTestSnapshot(t, r)
	if len(s.Files) != 1 || !s.Files[0].Conflict {
		t.Fatal(s.Files)
	}
	if err := r.stage([]sourceFile{{Path: name}}); err == nil {
		t.Fatal("unresolved file staged")
	}
	if err := r.commit("unresolved"); err == nil {
		t.Fatal("unresolved commit accepted")
	}
	sourceTestWrite(t, r, "resources/untracked.txt", "keep unstaged\n")
	if err := r.commit("blocked"); err == nil {
		t.Fatal("auto staging accepted a conflicted merge")
	}
	if got := sourceTestGit(t, r, "ls-files", "--", "resources/untracked.txt"); got != "" {
		t.Fatal("blocked commit modified the index")
	}
	if err := os.Remove(filepath.Join(r.Root, "resources/untracked.txt")); err != nil {
		t.Fatal(err)
	}
	tab, err := r.openDiff(s.Files[0], "merge", "")
	if err != nil {
		t.Fatal(err)
	}
	if tab.Local != "before\nlocal\nafter\n" || tab.Incoming != "before\nincoming\nafter\n" || !sourceHasMarkers(tab.Result) {
		t.Fatal(tab)
	}
	if err := r.saveMerge(*tab); err == nil {
		t.Fatal("saved unresolved merge")
	}
	tab.Result = resolveSourceConflict(tab.Result, 0, "both")
	if tab.Result != "before\nlocal\nincoming\nafter\n" {
		t.Fatal(tab.Result)
	}
	if err := r.saveMerge(*tab); err != nil {
		t.Fatal(err)
	}
	if s := sourceTestSnapshot(t, r); s.Files[0].Conflict || s.Files[0].Index != 'M' {
		t.Fatal(s.Files)
	}
	if err := r.commit("resolved"); err != nil {
		t.Fatal(err)
	}
	s = sourceTestSnapshot(t, r)
	if len(s.Files) != 0 || len(strings.Fields(s.Commits[0].Parents)) != 2 {
		t.Fatal(s)
	}
	files, err := r.commitFiles(s.Commits[0].Hash)
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
}

func TestSourceControlMergeExternalEditAndPaths(t *testing.T) {
	r := sourceTestRepository(t)
	sourceTestWrite(t, r, "file.txt", "original")
	tab := sourceTab{Path: "file.txt", Disk: "original", Result: "resolved"}
	sourceTestWrite(t, r, "file.txt", "external")
	if err := r.saveMerge(tab); err == nil {
		t.Fatal("overwrote external edit")
	}
	for _, name := range []string{"../outside", "/tmp/outside", ".", ""} {
		if _, err := r.validatePath(name); err == nil {
			t.Fatal("invalid path accepted", name)
		}
	}
}

func TestSourceControlDiffAlignment(t *testing.T) {
	for _, test := range []struct {
		Name, Left, Right string
		Deleted, Added    int
	}{
		{"replace", "a\nb\nc\n", "a\nx\nc\n", 1, 1},
		{"insert start", "a\n", "x\na\n", 0, 1},
		{"insert end", "a\n", "a\nx\n", 0, 1},
		{"delete start", "x\na\n", "a\n", 1, 0},
		{"delete end", "a\nx\n", "a\n", 1, 0},
		{"new", "", "a\nb\n", 0, 2},
		{"deleted", "a\nb\n", "", 2, 0},
		{"empty lines", "\n\n", "\nx\n", 1, 1},
		{"newline only", "a", "a\n", 1, 1},
	} {
		t.Run(test.Name, func(t *testing.T) {
			rows, err := sourceDiff(newPencodeBackend(t), []byte(test.Left), []byte(test.Right))
			if err != nil {
				t.Fatal(err)
			}
			var left, right []string
			deleted, added := 0, 0
			for _, row := range rows {
				if row.LeftNumber > 0 {
					left = append(left, row.Left)
					if row.LeftNumber != len(left) {
						t.Fatal(rows)
					}
				}
				if row.RightNumber > 0 {
					right = append(right, row.Right)
					if row.RightNumber != len(right) {
						t.Fatal(rows)
					}
				}
				if row.LeftKind != sourceUnchanged {
					deleted++
				}
				if row.RightKind != sourceUnchanged {
					added++
				}
			}
			equalLine := func(text string, line sourceLine) bool { return text == line.Text }
			if !slices.EqualFunc(left, sourceLines(test.Left), equalLine) || !slices.EqualFunc(right, sourceLines(test.Right), equalLine) || deleted != test.Deleted || added != test.Added {
				t.Fatal(rows, deleted, added)
			}
		})
	}
}

func TestSourceControlResolveDiff3CRLF(t *testing.T) {
	value := "before\r\n<<<<<<< HEAD\r\nlocal\r\n||||||| base\r\nbase\r\n=======\r\nincoming\r\n>>>>>>> other\r\nafter\r\n"
	for side, want := range map[string]string{"local": "local\r\n", "incoming": "incoming\r\n", "both": "local\r\nincoming\r\n"} {
		if got := resolveSourceConflict(value, 0, side); got != "before\r\n"+want+"after\r\n" {
			t.Fatalf("%s: %q", side, got)
		}
	}
}

func TestSourceControlGraphEdges(t *testing.T) {
	commits := []sourceCommit{{Hash: "merge", Parents: "left right"}, {Hash: "left", Parents: "base"}, {Hash: "right", Parents: "base"}, {Hash: "base"}}
	var graph sourceGraph
	for i := range commits {
		graph.append(&commits[i])
	}
	if len(commits[0].Edges) != 2 || commits[2].Lane != 1 || len(commits[2].Edges) != 2 || commits[2].Edges[1].To != 1 {
		t.Fatal(commits)
	}
	if joins := commits[3].Joins; len(joins) != 1 || joins[0].From != 1 || joins[0].To != commits[3].Lane {
		t.Fatal("branches did not join at the base commit")
	}
	for i, c := range commits {
		if c.Incoming != (i > 0) {
			t.Fatalf("commit %q incoming=%t", c.Hash, c.Incoming)
		}
	}
}

func TestSourceControlGraphColorsSurviveLaneCompaction(t *testing.T) {
	commits := []sourceCommit{
		{Hash: "merge", Parents: "left middle right"},
		{Hash: "left"},
		{Hash: "middle", Parents: "base"},
		{Hash: "right", Parents: "base"},
		{Hash: "base"},
	}
	var graph sourceGraph
	for i := range commits {
		graph.append(&commits[i])
	}
	if commits[0].Color == commits[2].Color || commits[2].Color == commits[3].Color || commits[0].Color == commits[3].Color {
		t.Fatal("parallel branches share a color")
	}
	for i, edge := range commits[0].Edges {
		if edge.Color != commits[i+1].Color {
			t.Fatal("fork path does not match its branch dot")
		}
	}
	for i, edge := range commits[1].Edges {
		if edge.From != i+1 || edge.To != i || edge.Color != commits[i+2].Color {
			t.Fatalf("compaction changed a branch color: %+v", edge)
		}
	}
	if commits[2].Lane != 0 || commits[3].Lane != 1 || commits[4].Color != commits[2].Color {
		t.Fatal("branch identity followed its column rather than its history")
	}
	if edge := commits[3].Edges[1]; edge.To != 1 || edge.Color != commits[3].Color {
		t.Fatal("merge path lost its branch color")
	}
	if joins := commits[4].Joins; len(joins) != 1 || joins[0].Color != commits[3].Color || joins[0].To != commits[4].Lane {
		t.Fatal("merge arc lost its branch color or missed the parent dot")
	}
}

func TestSourceControlGraphReusesOnlyInactiveColors(t *testing.T) {
	var graph sourceGraph
	main := sourceCommit{Hash: "main", Parents: "base"}
	graph.append(&main)
	for range sourceGraphColorCount * 2 {
		tip := sourceCommit{Hash: "independent-root"}
		graph.append(&tip)
		if tip.Color == main.Color {
			t.Fatal("palette wrap reused an active branch color")
		}
	}
	base := sourceCommit{Hash: "base"}
	graph.append(&base)
	if base.Color != main.Color {
		t.Fatal("independent roots changed the main branch color")
	}
}
