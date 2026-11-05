package environment

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoginPATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fixture")
	}
	directory := t.TempDir()
	shell := filepath.Join(directory, "shell")
	tools := filepath.Join(directory, "tools")
	script := "#!/bin/sh\n[ \"$1\" = -ilc ] || exit 1\nexport PATH=\"$PATH:" + tools + "\"\nprintf 'profile noise\\n%s%s%s\\n' '" + pathStart + "' \"$PATH\" '" + pathEnd + "'\n"
	if err := os.WriteFile(shell, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	path := loginPATH(shell)
	if path == "" || path != os.Getenv("PATH")+":"+tools {
		t.Fatalf("login PATH = %q", path)
	}
}

func TestConfigureImportsPATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fixture")
	}
	directory := t.TempDir()
	shell := filepath.Join(directory, "shell")
	tools := filepath.Join(directory, "tools")
	script := "#!/bin/sh\n[ \"$1\" = -ilc ] || exit 1\nexport PATH=\"" + tools + ":$PATH\"\nprintf '%s%s%s' '" + pathStart + "' \"$PATH\" '" + pathEnd + "'\n"
	if err := os.WriteFile(shell, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)
	t.Setenv("PATH", "/minimal/bin")
	Configure()
	if got := os.Getenv("PATH"); got != tools+":/minimal/bin" {
		t.Fatalf("configured PATH = %q", got)
	}
}
