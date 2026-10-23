package remote

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pencode-labs/pencode/transport"
)

func TestSSHPlatformAndTargetValidation(t *testing.T) {
	platform, err := parsePlatform("pencode-ssh-v1\nLinux\naarch64\n")
	if err != nil || platform != (Platform{"linux", "arm64"}) {
		t.Fatal(platform, err)
	}
	for _, output := range []string{"banner\npencode-ssh-v1\nLinux\nx86_64\n", "pencode-ssh-v1\nWindows\namd64", "pencode-ssh-v1\nLinux\nmips"} {
		if _, err := parsePlatform(output); err == nil {
			t.Fatalf("accepted platform output %q", output)
		}
	}
	for _, host := range []string{"", "-oProxyCommand=bad", "host;touch /tmp/bad", "host\nother", "$(command)"} {
		if err := (Target{Host: host}).Validate(); err == nil {
			t.Fatalf("accepted SSH option or command injection %q", host)
		}
	}
	if err := (Target{Host: "user@host-alias", ServerRoot: "/tmp/a ' directory"}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSSHConnectReusesInstalledCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX bootstrap fixture")
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip(err)
	}
	for _, fixture := range []struct {
		name       string
		serverMode os.FileMode
		pencode    bool
		homeServer bool
		want       string
	}{
		{name: "server", serverMode: 0o700, want: "pencode-server:\n"},
		{name: "pencode", pencode: true, want: "pencode:--pencode-server\n"},
		{name: "prefer server", serverMode: 0o700, pencode: true, want: "pencode-server:\n"},
		{name: "nonexecutable server", serverMode: 0o600, pencode: true, want: "pencode:--pencode-server\n"},
		{name: "home server", homeServer: true, want: "home-server:\n"},
		{name: "prefer PATH server over home", serverMode: 0o700, homeServer: true, want: "pencode-server:\n"},
		{name: "prefer PATH pencode over home", pencode: true, homeServer: true, want: "pencode:--pencode-server\n"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "remote's bin")
			if err := os.Mkdir(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(shell, filepath.Join(directory, "sh")); err != nil {
				t.Fatal(err)
			}
			home := filepath.Join(t.TempDir(), "remote's home")
			if err := os.MkdirAll(filepath.Join(home, ".pencode"), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", home)
			// No platform or installation utilities are available in this PATH.
			t.Setenv("PATH", directory)
			writeCommand := func(path, script string, mode os.FileMode) string {
				t.Helper()
				if err := os.WriteFile(path, []byte("#!"+shell+"\n"+script), mode); err != nil {
					t.Fatal(err)
				}
				return path
			}
			peer := func(name string) string {
				return "printf '" + name + ":%s\\n' \"$*\"\nIFS= read -r request\nprintf '%s\\n' \"$request\"\n"
			}
			if fixture.serverMode != 0 {
				writeCommand(filepath.Join(directory, "pencode-server"), peer("pencode-server"), fixture.serverMode)
			}
			if fixture.pencode {
				writeCommand(filepath.Join(directory, "pencode"), peer("pencode"), 0o700)
			}
			if fixture.homeServer {
				writeCommand(filepath.Join(home, ".pencode", "pencode-server"), peer("home-server"), 0o700)
			}
			ssh := writeCommand(filepath.Join(directory, "ssh"), "for command do :; done\nexec sh -c \"$command\"\n", 0o700)
			root := filepath.Join(directory, "unneeded install")
			serverRoot := root
			if fixture.homeServer {
				serverRoot = ""
			}
			var progress []string
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			stream, err := (Connector{Executable: ssh}).Connect(ctx, Target{
				Host: "fixture", ServerRoot: serverRoot, ServerBinary: filepath.Join(directory, "missing local binary"),
			}, func(message string) { progress = append(progress, message) })
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			if _, err := io.WriteString(stream, "client payload\n"); err != nil {
				t.Fatal(err)
			}
			output, err := io.ReadAll(stream)
			if err != nil || string(output) != fixture.want+"client payload\n" {
				t.Fatalf("installed server stream: %q, %v", output, err)
			}
			if _, err := os.Stat(root); !os.IsNotExist(err) {
				t.Fatal("installed server created an installation directory", err)
			}
			if strings.Contains(strings.Join(progress, ""), "上传") {
				t.Fatal("installed server triggered an upload", progress)
			}
		})
	}
}

func TestSSHAtomicInstallReuseAndFailureCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX bootstrap script runs on the SSH host")
	}
	for _, name := range []string{"default directory", "custom directory"} {
		t.Run(name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "remote's home")
			t.Setenv("HOME", home)
			root := filepath.Join(home, ".pencode")
			target := Target{Host: "fixture"}
			if name == "custom directory" {
				root = filepath.Join(t.TempDir(), `server's \directory with spaces`)
				target.ServerRoot = root
			}
			data := []byte("#!/bin/sh\nprintf 'server ready\\n'\n")
			hash := sha256.Sum256(data)
			digest := hex.EncodeToString(hash[:])
			run := func(payload []byte) (string, error) {
				t.Helper()
				var output bytes.Buffer
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				var input io.Reader
				if payload != nil {
					input = io.MultiReader(strings.NewReader(fmt.Sprintf("%s\n%d\n", digest, len(payload))), bytes.NewReader(payload))
				}
				err := transport.Run(ctx, input, &output, &output, "sh", "-c", bootstrapCommand(target))
				return output.String(), err
			}
			if output, err := run(data); err != nil || !strings.HasSuffix(output, "missing\nstarted\nserver ready\n") {
				t.Fatal(output, err)
			}
			if output, err := run(nil); err != nil || output != "installed\nstarted\nserver ready\n" {
				t.Fatal("installed server was not reused without local metadata", output, err)
			}
			installed := filepath.Join(root, "pencode-server")
			if err := os.WriteFile(installed, []byte("incomplete"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(installed, 0o600); err != nil {
				t.Fatal(err)
			}
			if output, err := run([]byte("corrupted transfer")); err == nil || !strings.Contains(output, "missing\n") {
				t.Fatal("published bytes with the wrong checksum", output, err)
			}
			got, err := os.ReadFile(installed)
			if err != nil || string(got) != "incomplete" {
				t.Fatal("failed upload replaced the existing file", err)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 1 || entries[0].Name() != "pencode-server" {
				t.Fatal("failed install retained staging files", entries, err)
			}
			if output, err := run(data); err != nil || !strings.HasSuffix(output, "missing\nstarted\nserver ready\n") {
				t.Fatal("incomplete binary was not replaced", output, err)
			}
			got, err = os.ReadFile(installed)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatal("server was not published at the fixed path", err)
			}
		})
	}
}

func TestSSHArtifactRejectsOtherGoPrograms(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = (Connector{}).artifact(Target{ServerBinary: self}, Platform{runtime.GOOS, runtime.GOARCH})
	if err == nil {
		t.Fatal("accepted a test binary as a standalone pencode-server")
	}
}

func TestSSHBootstrapCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX bootstrap fixture")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := transport.Run(ctx, nil, nil, nil, "sh", "-c", "sleep 30")
	if err == nil || time.Since(started) > 3*time.Second {
		t.Fatal("SSH bootstrap did not cancel its child group", err)
	}
}

func TestSSHOutputIsBounded(t *testing.T) {
	var output boundedOutput
	if n, err := output.Write([]byte(strings.Repeat("x", 64<<10))); err != nil || n != 64<<10 || len(output.String()) != 4096 {
		t.Fatal("diagnostics were not drained and bounded", n, err)
	}
}
