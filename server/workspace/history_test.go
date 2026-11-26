package workspace

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/pencode-labs/pencode/configuration"
	"github.com/pencode-labs/pencode/protocol"
)

func TestFolderHistoryRestoresDeletedVersionAcrossReopen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		config := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", config)
		t.Setenv("APPDATA", config)
		s, p := fixture(t)
		for _, folder := range []string{"src", "src/sub", "src2"} {
			if err := s.Create(p.ID, folder, true); err != nil {
				t.Fatal(err)
			}
		}
		for _, name := range []string{"src/sub/blob", "src2/empty"} {
			if _, err := s.CreateDocument(p.ID, name, ""); err != nil {
				t.Fatal(err)
			}
		}
		write(t, p, "src/untouched", "generated")
		time.Sleep(11 * time.Second)
		const content = "\x00\xff\n\x01"
		write(t, p, "src/sub/blob", content)
		s.History.Saved(s.projects[p.ID].root, "src/sub/blob", int64(len(content)), nil)
		if err := s.Remove(p.ID, "src"); err != nil {
			t.Fatal(err)
		}
		s.Close()
		s = new(Store)
		defer s.Close()
		p, err := s.Open(p.Path)
		if err != nil {
			t.Fatal(err)
		}
		list := func(scope string, recursive bool, count int) []protocol.FileVersion {
			t.Helper()
			page, more, err := s.History.List(t.Context(), p.Path, scope, recursive, protocol.FileVersion{})
			if err != nil || more || len(page) != count {
				t.Fatalf("scope %s: %v %v %v", scope, page, more, err)
			}
			return page
		}
		list("src", true, 2)
		list("", true, 3)
		page := list("src/sub/blob", false, 2)
		root := s.projects[p.ID].root
		for i, want := range []string{content, ""} {
			if err := s.History.Restore(root, page[i]); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(p.Path, "src", "sub", "blob"))
			if err != nil || string(data) != want {
				t.Fatalf("restored %q: %v", data, err)
			}
		}
		if _, err := root.Stat("src/untouched"); !os.IsNotExist(err) {
			t.Fatal("restored a sibling")
		}
		if err := s.History.Restore(root, protocol.FileVersion{Path: "../outside", Time: page[0].Time}); err == nil {
			t.Fatal("accepted invalid restore path")
		}
	})
}

func TestFileHistoryRetentionAndStablePages(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		config := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", config)
		t.Setenv("APPDATA", config)
		s, p := fixture(t)
		root := s.projects[p.ID].root
		settings := configuration.DefaultLocalHistory()
		for _, name := range []string{"one", "two", "three"} {
			for i := range settings.MaxFileEntries + 2 {
				write(t, p, name, strconv.Itoa(i))
				time.Sleep(11 * time.Second)
				if err := captureHistory(root, name, int64(len(strconv.Itoa(i))), nil, historySave, settings); err != nil {
					t.Fatal(err)
				}
			}
		}
		first, more, err := s.History.List(t.Context(), p.Path, "", true, protocol.FileVersion{})
		if err != nil || !more || len(first) != protocol.FileHistoryPageSize {
			t.Fatalf("first page: %d, more=%v, err=%v", len(first), more, err)
		}
		// A new version before the cursor must not shift the following page.
		write(t, p, "four", "new")
		s.History.Saved(root, "four", 3, nil)
		next, more, err := s.History.List(t.Context(), p.Path, "", true, first[len(first)-1])
		if err != nil || more || len(next) != 3*settings.MaxFileEntries-len(first) {
			t.Fatalf("next page: %d, more=%v, err=%v", len(next), more, err)
		}
		if !slices.IsSortedFunc(append(first, next...), compareVersions) {
			t.Fatal("versions are out of order")
		}
		if err := s.History.Restore(root, next[len(next)-1]); err != nil {
			t.Fatal(err)
		}
		if data, err := root.ReadFile("one"); err != nil || string(data) != "2" {
			t.Fatalf("oldest retained version = %q, %v", data, err)
		}
	})
}

func TestFileHistoryDiskLimitsAndSettings(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		config := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", config)
		t.Setenv("APPDATA", config)
		s, p := fixture(t)
		h := &FileHistory{SettingsPath: filepath.Join(config, "settings.json")}
		s.History = h
		root := s.projects[p.ID].root
		save := func(name, content string) {
			t.Helper()
			write(t, p, name, content)
			h.Saved(root, name, int64(len(content)), nil)
		}
		check := func(name string, want ...string) {
			t.Helper()
			dir, err := historyLocation(p.Path, name)
			if err != nil {
				t.Fatal(err)
			}
			files, err := os.ReadDir(dir)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if len(files) != len(want) {
				t.Fatalf("%s: %d files on disk, want %d", name, len(files), len(want))
			}
			for i, f := range files {
				data, err := os.ReadFile(filepath.Join(dir, f.Name()))
				if err != nil || string(data) != want[i] {
					t.Fatalf("%s: wrong version %d: %v", name, i, err)
				}
			}
		}
		limit := strings.Repeat("a", 256<<10)
		save("small", limit)
		check("small", limit)
		save("big", limit+"x")
		check("big")
		time.Sleep(10 * time.Second)
		save("small", "b")
		time.Sleep(8 * time.Second)
		save("small", "c")
		check("small", "c")
		time.Sleep(10*time.Second + time.Nanosecond)
		save("small", "d")
		check("small", "c", "d")
		if err := configuration.Save(t.Context(), config, []byte(`{"workbench.localHistory.maxFileSize":1,"workbench.localHistory.maxFileEntries":1,"workbench.localHistory.exclude":{"**/*.{log,tmp}":true,"small":false}}`)); err != nil {
			t.Fatal(err)
		}
		save("big", strings.Repeat("x", 1025))
		save("generated.log", "build")
		save("small", "e")
		check("big")
		check("generated.log")
		check("small", "e")
		if err := configuration.Save(t.Context(), config, []byte(`{"workbench.localHistory.enabled":false}`)); err != nil {
			t.Fatal(err)
		}
		save("small", "off")
		check("small", "e")
		if err := configuration.Save(t.Context(), config, []byte(`{"workbench.localHistory.maxFileEntries":0}`)); err != nil {
			t.Fatal(err)
		}
		versions, _, err := h.List(t.Context(), p.Path, "small", false, protocol.FileVersion{})
		if err != nil || len(versions) != 0 {
			t.Fatalf("zero retention: %v, %v", versions, err)
		}
		check("small")
	})
}

func TestMoveOnlySavedHistory(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		config := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", config)
		t.Setenv("APPDATA", config)
		s, p := fixture(t)
		for _, dir := range []string{"from", "to"} {
			if err := s.Create(p.ID, dir, true); err != nil {
				t.Fatal(err)
			}
			doc, err := s.CreateDocument(p.ID, dir+"/saved", dir)
			if err != nil {
				t.Fatal(err)
			}
			s.CloseDocument(doc.ID)
		}
		write(t, p, "from/untracked", "external")
		if err := s.Remove(p.ID, "to"); err != nil {
			t.Fatal(err)
		}
		if err := s.Rename(p.ID, "from", "to"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Transfer(p.ID, p.ID, "to", "", false, true); err != nil {
			t.Fatal(err)
		}
		entries, _, err := s.History.List(t.Context(), p.Path, "", true, protocol.FileVersion{})
		if err != nil || len(entries) != 2 {
			t.Fatalf("moved history: %v, %v", entries, err)
		}
		for _, v := range entries {
			if v.Path != "to/saved" {
				t.Fatalf("unexpected history for %s", v.Path)
			}
		}
		if entries[0].Time == entries[1].Time {
			t.Fatal("moved versions share an ID")
		}
		for i, want := range []string{"from", "to"} {
			data, err := ReadFileHistory(p.Path, entries[i])
			if err != nil || string(data) != want {
				t.Fatalf("moved entry %d = %q, %v", i, data, err)
			}
		}
	})
}
