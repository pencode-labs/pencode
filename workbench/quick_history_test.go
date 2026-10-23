package workbench

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestPencodeQuickHistoryPersistsOrderAndProjectIsolation(t *testing.T) {
	directory := t.TempDir()
	history := loadPencodeQuickHistory(directory)
	for _, entry := range [...]pencodeQuickHistoryEntry{
		{"project-a", "中文 file.go"},
		{"project-a", "second.go"},
		{"project-b", "中文 file.go"},
		{"project-a", "中文 file.go"},
	} {
		if err := history.remember(entry.project, entry.path); err != nil {
			t.Fatal(err)
		}
	}
	loaded := loadPencodeQuickHistory(directory)
	if loaded.count != 3 || loaded.rank("project-a", "中文 file.go") != 0 || loaded.rank("project-b", "中文 file.go") != 1 || loaded.rank("project-a", "second.go") != 2 {
		t.Fatalf("history lost recency, deduplication or project identity: %+v", loaded.entries[:loaded.count])
	}
	if loaded.rank("project-c", "中文 file.go") != pencodeQuickHistoryLimit {
		t.Fatal("history leaked between projects")
	}
	if err := loaded.remember("project-a", "second.go"); err != nil {
		t.Fatal(err)
	}
	if reloaded := loadPencodeQuickHistory(directory); reloaded.count != 3 || reloaded.rank("project-a", "second.go") != 0 {
		t.Fatal("reopening a historical file did not persist its new position")
	}
}

func TestPencodeQuickHistoryRanking(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	history := loadPencodeQuickHistory("")
	for _, path := range [...]string{"file-10.go", "file-11.go", "deleted.go", "directory.go"} {
		if err := history.remember(a, path); err != nil {
			t.Fatal(err)
		}
	}
	if err := history.remember(b, "file-09.go"); err != nil {
		t.Fatal(err)
	}
	backend := newPencodeBackend(t)
	projects := []*pencodeProject{{Path: a}, {Path: b}}
	for _, p := range projects {
		attachPencodeProject(t, p, backend)
		for index := range 12 {
			if err := os.WriteFile(filepath.Join(p.Path, fmt.Sprintf("file-%02d.go", index)), nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Mkdir(filepath.Join(p.Path, "directory.go"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	model := workspaceModel{backend: backend, projects: projects, activeProjectIndex: 0, projectPath: a, session: &Session{quickHistory: history}}
	for _, p := range projects {
		model.startProjectScan(p, true)
		waitPencodeProjectScan(t, p, func() { model.pollProjectScans() })
	}
	model.openQuickOpen()
	settleQuick(t, &model)
	want := [...]string{"file-11.go", "file-10.go", "file-00.go", "file-01.go", "file-02.go", "file-03.go", "file-04.go", "file-05.go"}
	if model.quickMatches != want {
		t.Fatalf("history should rank before ordinary matches, skipping unavailable files: %v", model.quickMatches)
	}
	model.quickQuery = "file-10:51"
	settleQuick(t, &model)
	if model.quickCount != 1 || model.quickMatches[0] != "file-10.go" {
		t.Fatal("history bypassed the filename filter")
	}
	model.activeProjectIndex = 1
	model.projectPath, model.quickQuery = b, ""
	settleQuick(t, &model)
	if model.quickMatches[0] != "file-09.go" || model.quickMatches[1] != "file-00.go" {
		t.Fatalf("history ranking crossed project boundaries: %v", model.quickMatches)
	}
	if allocations := testing.AllocsPerRun(100, func() {
		model.refreshQuickMatches()
		model.pollQuickMatches()
	}); allocations != 0 {
		t.Fatalf("history ranking allocated %g times", allocations)
	}
}

func TestPencodeQuickHistoryBoundedAndCorruptCache(t *testing.T) {
	history := loadPencodeQuickHistory("")
	for index := range pencodeQuickHistoryLimit + 10 {
		if err := history.remember("project", fmt.Sprintf("file-%d.go", index)); err != nil {
			t.Fatal(err)
		}
	}
	if history.count != pencodeQuickHistoryLimit || len(history.ranks) != pencodeQuickHistoryLimit || history.rank("project", "file-0.go") != pencodeQuickHistoryLimit {
		t.Fatal("history did not evict its oldest files")
	}
	if allocations := testing.AllocsPerRun(100, func() {
		_ = history.remember("project", "file-130.go")
		_ = history.remember("project", "file-131.go")
	}); allocations != 0 {
		t.Fatalf("updating in-memory recency allocated %g times", allocations)
	}
	directory := t.TempDir()
	for _, data := range []string{"invalid header", pencodeQuickHistoryHeader + "\xff", pencodeQuickHistoryHeader + "\x07project\x08short", pencodeQuickHistoryHeader + "\x07project\x07file.go\xff"} {
		if err := os.WriteFile(filepath.Join(directory, "quick-open.history"), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		loaded := loadPencodeQuickHistory(directory)
		if loaded.count != 0 || len(loaded.ranks) != 0 {
			t.Fatal("corrupt cache left partial history")
		}
		if err := loaded.remember("project", "recovered.go"); err != nil {
			t.Fatal(err)
		}
		if loadPencodeQuickHistory(directory).rank("project", "recovered.go") != 0 {
			t.Fatal("cache could not recover after corrupt data")
		}
	}
}

func TestPencodeQuickHistoryRetriesFailedWrite(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "cache")
	if err := os.WriteFile(directory, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	history := loadPencodeQuickHistory(directory)
	if err := history.remember("project", "main.go"); err == nil {
		t.Fatal("blocked cache directory should fail to save")
	}
	if err := os.Remove(directory); err != nil {
		t.Fatal(err)
	}
	if err := history.remember("project", "main.go"); err != nil {
		t.Fatal(err)
	}
	if loadPencodeQuickHistory(directory).rank("project", "main.go") != 0 {
		t.Fatal("opening the latest file again did not retry the failed write")
	}
}
