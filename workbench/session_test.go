package workbench

import (
	"path/filepath"
	"testing"

	"github.com/pencode-labs/pencode/client"
)

// Keep test history out of user storage and independent of other tests.
func newPencodeTestSession(t testing.TB, backend *client.Client) *Session {
	t.Helper()
	directory := t.TempDir()
	return newSession(backend, filepath.Join(directory, "cache"), filepath.Join(directory, "config"))
}

func TestSessionHistoryStorageIsolation(t *testing.T) {
	directory := t.TempDir()
	for _, name := range [...]string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA"} {
		t.Setenv(name, directory)
	}
	remember := func(s *Session, path string) {
		t.Helper()
		entry := pencodeRecentProject{Path: path}
		entry.prepare()
		if err := s.recentProjects.remember(entry); err != nil {
			t.Fatal(err)
		}
		if err := s.quickHistory.remember(path, "main.go"); err != nil {
			t.Fatal(err)
		}
	}
	user := NewSession(nil)
	remember(user, "/projects/user")
	for range 2 {
		session := newPencodeTestSession(t, nil)
		if session.recentProjects.count != 0 || session.quickHistory.count != 0 {
			t.Fatal("test session inherited existing history")
		}
		remember(session, "/projects/test")
		recent := loadPencodeRecentProjects(filepath.Dir(session.recentProjects.file))
		quick := loadPencodeQuickHistory(filepath.Dir(session.quickHistory.file))
		if recent.count != 1 || recent.entries[0].Path != "/projects/test" || quick.count != 1 || quick.entries[0].project != "/projects/test" {
			t.Fatal("test session history was not persisted in its own storage")
		}
	}
	user = NewSession(nil)
	if user.recentProjects.count != 1 || user.recentProjects.entries[0].Path != "/projects/user" || user.quickHistory.count != 1 || user.quickHistory.entries[0].project != "/projects/user" {
		t.Fatal("test sessions changed user history")
	}
}
