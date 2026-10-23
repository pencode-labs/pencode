package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/diiyw/onix/projects/pencode/protocol"
)

func fixture(t *testing.T) (*Store, protocol.Workspace) {
	t.Helper()
	s := new(Store)
	t.Cleanup(s.Close)
	p, err := s.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s, p
}
func write(t *testing.T, p protocol.Workspace, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(p.Path, name), []byte(content), 0640); err != nil {
		t.Fatal(err)
	}
}
func assertCode(t *testing.T, err error, code protocol.Code) {
	t.Helper()
	var e *protocol.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("error=%v, want code %d", err, code)
	}
}

func TestVersionedAtomicSaveAndExternalConflict(t *testing.T) {
	s, p := fixture(t)
	write(t, p, "main.go", "a中\r\n")
	d, err := s.OpenDocument(p.ID, "main.go", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Change(d.ID, 1, 2, 3, "x"); err == nil {
		t.Fatal("accepted split rune")
	}
	v, err := s.Change(d.ID, 1, 1, 4, "文🙂")
	if err != nil {
		t.Fatal(err)
	}
	assertCode(t, s.Save(d.ID, 1), protocol.VersionMismatch)
	if err := s.Save(d.ID, v); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(p.Path, "main.go"))
	if err != nil || string(data) != "a文🙂\r\n" {
		t.Fatal(string(data), err)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(filepath.Join(p.Path, "main.go"))
		if info.Mode().Perm() != 0640 {
			t.Fatal("save changed file mode")
		}
	}
	write(t, p, "main.go", "external")
	assertCode(t, s.Save(d.ID, v), protocol.Conflict)
	data, _ = os.ReadFile(filepath.Join(p.Path, "main.go"))
	if string(data) != "external" {
		t.Fatal("overwrote external change")
	}
	entries, _ := os.ReadDir(p.Path)
	if len(entries) != 1 {
		t.Fatal("temporary save file leaked")
	}
}

func TestRootsBinaryFilesAndLifetime(t *testing.T) {
	s, p := fixture(t)
	write(t, p, "image.png", "\x00\xff\xfe")
	d, err := s.OpenDocument(p.ID, "image.png", false)
	if err != nil || d.ID != 0 || d.Content != "" || len(s.documents) != 0 {
		t.Fatal(d, err)
	}
	for _, name := range []string{"../escape", "/etc/passwd", "a/../image.png", "a\\b", "."} {
		if _, err := s.OpenDocument(p.ID, name, true); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	if _, err := s.OpenDocument(p.ID, "missing", true); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	write(t, p, "text", "hello")
	d, err = s.OpenDocument(p.ID, "text", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CloseProject(p.ID); err != nil {
		t.Fatal(err)
	}
	assertCode(t, s.Save(d.ID, d.Version), protocol.NotFound)
	if s.bytes != 0 || len(s.documents) != 0 {
		t.Fatal("closed workspace retained documents")
	}
}

func TestSymlinkAndMovedUnsavedDocument(t *testing.T) {
	s, p := fixture(t)
	write(t, p, "original", "saved")
	d, err := s.OpenDocument(p.ID, "original", true)
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.Change(d.ID, d.Version, 0, 5, "unsaved")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(p.Path, "original"), filepath.Join(p.Path, "moved")); err != nil {
		t.Fatal(err)
	}
	if err := s.Move(d.ID, p.ID, "moved"); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(d.ID, v); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(p.Path, "moved"))
	if string(data) != "unsaved" {
		t.Fatal(string(data))
	}
	if runtime.GOOS == "windows" {
		return
	}
	if err := os.Symlink("moved", filepath.Join(p.Path, "link")); err != nil {
		t.Fatal(err)
	}
	linked, err := s.OpenDocument(p.ID, "link", true)
	if err != nil {
		t.Fatal(err)
	}
	v, err = s.Change(linked.ID, linked.Version, 0, 7, "through link")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(linked.ID, v); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Readlink(filepath.Join(p.Path, "link")); err != nil {
		t.Fatal("save replaced the symlink")
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(p.Path, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenDocument(p.ID, "escape", true); err == nil {
		t.Fatal("read escaped root")
	}
}

func TestWarmDocumentChangesDoNotAllocate(t *testing.T) {
	s, p := fixture(t)
	write(t, p, "text", "base")
	d, err := s.OpenDocument(p.ID, "text", true)
	if err != nil {
		t.Fatal(err)
	}
	version := d.Version
	if n := testing.AllocsPerRun(100, func() {
		var err error
		version, err = s.Change(d.ID, version, 0, 1, "x")
		if err != nil {
			panic(err)
		}
	}); n != 0 {
		t.Fatalf("warm document edit allocated %g times", n)
	}
}
