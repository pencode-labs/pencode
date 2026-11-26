package workbench

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pencode-labs/pencode/protocol"
	"github.com/pencode-labs/pencode/server/workspace"
)

func TestDiskStorageSettingsSelectiveCleanup(t *testing.T) {
	home := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CACHE_HOME", "LOCALAPPDATA"} {
		t.Setenv(key, home)
	}
	t.Setenv("PENCODE_PROFILE_DIR", "")
	app, m, _, state := languageInteractionFixture(t)
	dir, err := workspace.HistoryDirectory()
	if err != nil {
		t.Fatal(err)
	}
	revisions := filepath.Join(dir, "tree", "test")
	if err := os.MkdirAll(revisions, 0700); err != nil {
		t.Fatal(err)
	}
	put := func(stamp, size int) string {
		t.Helper()
		name := filepath.Join(revisions, fmt.Sprintf("%016x-600-%s", stamp, strings.Repeat("0", 64)))
		if err := os.WriteFile(name, make([]byte, size), 0600); err != nil {
			t.Fatal(err)
		}
		return name
	}
	large, small := put(1, 256<<10+1), put(2, 256<<10)
	m.settingsOpen = true
	markPencodeRootDirtyByModel(m.fontResources, m)
	languageInteractionStep(t, app)
	click := func(id string) { t.Helper(); editorContextClick(t, app, m, state, id) }
	wait := func() {
		t.Helper()
		for stop := time.Now().Add(5 * time.Second); m.storage == nil || !m.storage.ready || m.storage.cancel != nil; {
			if time.Now().After(stop) {
				t.Fatalf("storage request timed out: %+v", m.storage)
			}
			languageInteractionStep(t, app)
		}
	}
	click("settings-storage-nav")
	wait()
	if m.storage.report.Sizes[protocol.StorageLargeHistory].Bytes != 256<<10+1 || m.storage.report.Problem != "" {
		t.Fatalf("scan: %+v", m.storage.report)
	}
	click("storage-large")
	if m.storage.selected != protocol.StorageLargeHistory.Mask() {
		t.Fatal("wrong selection", m.storage.selected)
	}
	click("storage-clean")
	if !m.storage.confirm {
		t.Fatal("missing confirmation")
	}
	click("storage-cancel")
	if _, err := os.Stat(large); err != nil {
		t.Fatal("cancel removed history", err)
	}
	click("storage-clean")
	click("storage-clean")
	wait()
	if _, err := os.Stat(large); !os.IsNotExist(err) {
		t.Fatal("large history survived", err)
	}
	if _, err := os.Stat(small); err != nil {
		t.Fatal("unselected history removed", err)
	}
	if m.storage.report.Removed.Files != 1 || m.storage.report.Sizes[protocol.StorageLargeHistory].Bytes != 0 {
		t.Fatalf("cleanup: %+v", m.storage.report)
	}
	click("storage-refresh")
	wait()
	if m.storage.selected != 0 || m.storage.report.Removed.Files != 0 {
		t.Fatal("stale selection or result")
	}
	click("settings-appearance-nav")
	if m.storage != nil {
		t.Fatal("inactive settings retained selection")
	}
}

func TestStorageSelectionFollowsBackend(t *testing.T) {
	base := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CACHE_HOME", "LOCALAPPDATA", "XDG_CONFIG_HOME", "APPDATA"} {
		t.Setenv(key, base)
	}
	t.Setenv("PENCODE_PROFILE_DIR", "")
	local, remote := settingsBackend(t, t.TempDir()), settingsBackend(t, t.TempDir())
	m := workspaceModel{backend: local}
	m.settingsOpen, m.settingsToolPage = true, diskStorageSettings
	wait := func() {
		t.Helper()
		for stop := time.Now().Add(5 * time.Second); ; {
			m.pollStorage()
			if m.storage != nil && m.storage.ready {
				return
			}
			if time.Now().After(stop) {
				t.Fatalf("storage scan timed out: %+v", m.storage)
			}
			time.Sleep(time.Millisecond)
		}
	}
	wait()
	m.storage.selected, m.storage.confirm = protocol.StorageHistory.Mask(), true
	previous := m.storage
	m.backend = remote
	m.handleStorage("storage-clean", false)
	if previous.cleaning {
		t.Fatal("confirmed cleanup was sent after switching hosts")
	}
	wait()
	if m.storage == previous || m.storage.backend != remote || m.storage.confirm || m.storage.selected != 0 {
		t.Fatal("selection carried to a different host")
	}
}
