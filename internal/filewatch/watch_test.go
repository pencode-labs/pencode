package filewatch

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeFileChangesAndClose(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	w, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err = w.Add(root); err != nil {
		t.Fatal(err)
	}
	if !Recursive {
		if err = w.Add(nested); err != nil {
			t.Fatal(err)
		}
	}
	name := filepath.Join(nested, "文件.txt")
	if err = os.WriteFile(name, []byte("one"), 0600); err != nil {
		t.Fatal(err)
	}
	wait := func(path string, op Op) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case event := <-w.Events:
				if event.Name == path && event.Has(op) {
					return
				}
			case err := <-w.Errors:
				t.Fatal(err)
			case <-deadline:
				t.Fatalf("missing %d for %s", op, path)
			}
		}
	}
	wait(name, Create)
	if err = os.WriteFile(name, []byte("two"), 0600); err != nil {
		t.Fatal(err)
	}
	wait(name, Write)
	if err = os.Rename(name, name+".new"); err != nil {
		t.Fatal(err)
	}
	wait(name+".new", Create)
	if err = os.Remove(name + ".new"); err != nil {
		t.Fatal(err)
	}
	wait(name+".new", Remove)
	done := make(chan struct{})
	go func() { w.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("idle native watcher did not close")
	}
	w.Close()
	if err := w.Add(root); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed watcher accepted Add: %v", err)
	}
}
func TestNativeQueueOverflow(t *testing.T) {
	w, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	event := Event{Name: "file", Op: Write}
	for range cap(w.Events) + 1 {
		w.emit(event)
	}
	select {
	case err := <-w.Errors:
		if err != ErrOverflow {
			t.Fatal(err)
		}
	default:
		t.Fatal("full event queue silently discarded a change")
	}
}
