//go:build linux || darwin || windows

package terminal

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func awaitOutput(t *testing.T, s *Session, predicate func(string) bool) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var text strings.Builder
	var b [32768]byte
	for time.Now().Before(deadline) {
		if n := s.ReadOutput(b[:]); n > 0 {
			text.Write(b[:n])
			if predicate(text.String()) {
				return text.String()
			}
		}
		time.Sleep(time.Millisecond * 5)
	}
	t.Fatalf("terminal output timeout: %q", text.String())
	return ""
}
func closeSession(t *testing.T, s *Session) {
	t.Helper()
	s.Close()
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Error("terminal did not release workers")
	}
}
func TestNativeSessionInteractiveResizeInterruptAndExit(t *testing.T) {
	directory := t.TempDir()
	s := Start(directory, 83, 17, nil)
	defer closeSession(t, s)
	if runtime.GOOS == "windows" {
		for i, marker := range []string{"PENCODE_READY", "PENCODE_RESIZED"} {
			if i > 0 {
				s.Resize(91, 21)
			}
			if err := os.WriteFile(filepath.Join(directory, "terminal-output.txt"), []byte(marker+"\r\n"), 0600); err != nil {
				t.Fatal(err)
			}
			// The command contains no marker, so input echo cannot pass the check.
			if _, err := s.Write([]byte("type terminal-output.txt\r")); err != nil {
				t.Fatal(err)
			}
			awaitOutput(t, s, func(text string) bool { return strings.Contains(text, marker) })
		}
		if _, err := s.Write([]byte("exit 0\r")); err != nil {
			t.Fatal(err)
		}
		select {
		case <-s.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("exit hung")
		}
		if ready, exited, err := s.Status(); !ready || !exited || err != nil {
			t.Fatalf("normal shell exit: ready=%v exited=%v err=%v", ready, exited, err)
		}
		return
	}
	command := "printf '\\120\\105\\116_READY'; test -t 0 && test -t 1 && test -t 2 && printf '\\120\\105\\116_TTY'; stty size\r"
	if _, err := s.Write([]byte(command)); err != nil {
		t.Fatal(err)
	}
	awaitOutput(t, s, func(text string) bool {
		return strings.Contains(text, "PEN_READY") && strings.Contains(text, "PEN_TTY") && strings.Contains(text, "17 83")
	})
	s.Resize(91, 21)
	if _, err := s.Write([]byte("stty size\r")); err != nil {
		t.Fatal(err)
	}
	awaitOutput(t, s, func(text string) bool { return strings.Contains(text, "21 91") })
	if _, err := s.Write([]byte("sleep 30\r")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if _, err := s.Write([]byte{3}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write([]byte("printf '\\120\\105\\116_INTERRUPTED'\r")); err != nil {
		t.Fatal(err)
	}
	awaitOutput(t, s, func(text string) bool { return strings.Contains(text, "PEN_INTERRUPTED") })
	if _, err := s.Write([]byte("exit\r")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("exit hung")
	}
}
func TestSessionCloseDuringStartAndBackpressure(t *testing.T) {
	for range 5 {
		s := Start(t.TempDir(), 80, 24, nil)
		closeSession(t, s)
	}
	if runtime.GOOS == "windows" {
		return
	}
	s := Start(t.TempDir(), 80, 24, nil)
	if _, err := s.Write([]byte("yes x\r")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	closeSession(t, s)
}
func TestSessionStartFailure(t *testing.T) {
	s := Start(t.TempDir()+"/missing", 80, 24, nil)
	defer closeSession(t, s)
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("start failure hung")
	}
	ready, exited, err := s.Status()
	if !ready || !exited || err == nil {
		t.Fatal("missing startup failure")
	}
}
func TestByteRingWrap(t *testing.T) {
	r := byteRing{data: make([]byte, 8)}
	r.write([]byte("abcdef"))
	var b [16]byte
	if n := r.read(b[:4]); n != 4 || string(b[:n]) != "abcd" {
		t.Fatal("first read")
	}
	r.write([]byte("ghijklmn"))
	if n := r.read(b[:]); n != 8 || string(b[:n]) != "efghijkl" {
		t.Fatalf("wrapped read %q", b[:n])
	}
}
