package windowstate

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRecoverMultipleWindowsAndExplicitClose(t *testing.T) {
	directory := t.TempDir()
	for _, value := range []string{"first", "closed", "third"} {
		w, _, err := Open[string](directory, "", false)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Save(&value); err != nil {
			t.Fatal(err)
		}
		if value == "closed" {
			if err := w.Forget(); err != nil {
				t.Fatal(err)
			}
		}
		w.Release()
	}
	// A malformed record and an interrupted temporary write are isolated.
	if err := os.WriteFile(filepath.Join(directory, "damaged.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, ".state-interrupted"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, ids, err := Open[string](directory, "", true)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	if !first.Restored || len(ids) != 1 {
		t.Fatalf("restored=%v pending=%v", first.Restored, ids)
	}
	values := []string{first.Value}
	// Another ordinary application launch must not duplicate this recovery.
	live, pending, err := Open[string](directory, "", true)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Release()
	if live.Restored || len(pending) != 0 {
		t.Fatal("live windows were recovered twice")
	}
	if err := live.Forget(); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		w, _, err := Open[string](directory, id, false)
		if err != nil {
			t.Fatal(err)
		}
		values = append(values, w.Value)
		if _, _, err := Open[string](directory, id, false); err == nil {
			t.Fatal("duplicate explicit restore claimed a live window")
		}
		if err := w.Forget(); err != nil {
			t.Fatal(err)
		}
		w.Release()
	}
	slices.Sort(values)
	if !slices.Equal(values, []string{"first", "third"}) {
		t.Fatalf("recovered %v", values)
	}
	for _, id := range []string{"..", "../outside", `..\outside`, "/absolute"} {
		if _, _, err := Open[string](directory, id, false); err == nil {
			t.Fatalf("invalid ID accepted: %q", id)
		}
	}
}

func TestForceKilledWindowReleasesLease(t *testing.T) {
	if directory := os.Getenv("PENCODE_WINDOW_STATE_HELPER"); directory != "" {
		w, _, err := Open[string](directory, "", false)
		if err != nil {
			t.Fatal(err)
		}
		value := "force-killed window"
		if err := w.Save(&value); err != nil {
			t.Fatal(err)
		}
		fmt.Println(strings.TrimSuffix(filepath.Base(w.file), ".json"))
		var b [1]byte
		_, _ = os.Stdin.Read(b[:])
		return
	}
	directory := t.TempDir()
	command := exec.Command(os.Args[0], "-test.run=^TestForceKilledWindowReleasesLease$")
	command.Env = append(os.Environ(), "PENCODE_WINDOW_STATE_HELPER="+directory)
	command.Stderr = os.Stderr
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		t.Fatal("child failed before saving its recovery record")
	}
	id := scanner.Text()
	live, ids, err := Open[string](directory, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if live.Restored || len(ids) != 0 {
		t.Fatal("another process's live window was recovered")
	}
	if err := live.Forget(); err != nil {
		t.Fatal(err)
	}
	live.Release()
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	restored, _, err := Open[string](directory, id, false)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Release()
	if restored.Value != "force-killed window" {
		t.Fatalf("lost snapshot: %q", restored.Value)
	}
}
