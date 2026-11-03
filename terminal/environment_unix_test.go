//go:build linux || darwin

package terminal

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeSessionInheritsEnvironmentAndLoadsLoginProfile(t *testing.T) {
	for _, shell := range []struct{ name, profile string }{
		{"bash", ".bash_profile"},
		{"zsh", ".zprofile"},
	} {
		t.Run(shell.name, func(t *testing.T) {
			path, err := exec.LookPath(shell.name)
			if err != nil {
				t.Skipf("%s is unavailable: %v", shell.name, err)
			}
			home := t.TempDir()
			bin := filepath.Join(home, "bin")
			if err := os.Mkdir(bin, 0700); err != nil {
				t.Fatal(err)
			}
			profile := "export PENCODE_PROFILE='profile value'\nexport PATH=\"$HOME/bin:$PATH\"\n"
			if err := os.WriteFile(filepath.Join(home, shell.profile), []byte(profile), 0600); err != nil {
				t.Fatal(err)
			}
			program := "#!/bin/sh\nprintf '%s\\n' \"$PENCODE_INHERITED|$PENCODE_PROFILE|$TERM|$COLORTERM|$TERM_PROGRAM\"\n"
			if err := os.WriteFile(filepath.Join(bin, "pencode-env-check"), []byte(program), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("SHELL", path)
			t.Setenv("HOME", home)
			t.Setenv("ZDOTDIR", home)
			t.Setenv("PENCODE_INHERITED", "inherited value")
			t.Setenv("PENCODE_PROFILE", "")
			t.Setenv("TERM", "dumb")
			t.Setenv("COLORTERM", "")
			t.Setenv("TERM_PROGRAM", "parent")
			s := Start(home, 80, 24, nil)
			defer closeSession(t, s)
			if _, err := s.Write([]byte("pencode-env-check\n")); err != nil {
				t.Fatal(err)
			}
			awaitOutput(t, s, func(text string) bool {
				return strings.Contains(text, "inherited value|profile value|xterm-256color|truecolor|pencode")
			})
		})
	}
}
