package remote

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pencode-labs/pencode/client"
)

// Set PENCODE_SSH_TEST_SERVER to a native standalone build to exercise real
// OpenSSH against an isolated loopback sshd. No user SSH files are changed.
func TestSSHIntegration(t *testing.T) {
	binary := os.Getenv("PENCODE_SSH_TEST_SERVER")
	if binary == "" || runtime.GOOS == "windows" {
		t.Skip("set PENCODE_SSH_TEST_SERVER for isolated OpenSSH integration")
	}
	sshd, err := exec.LookPath("sshd")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	for _, name := range []string{"host", "identity"} {
		passphrase := ""
		if name == "identity" {
			passphrase = "integration-passphrase"
		}
		command := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", passphrase, "-f", filepath.Join(directory, name))
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("key fixture: %v: %s", err, output)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	// The isolated authorized key lives under the shared system temp directory.
	// Disable sshd ownership traversal for this fixture only.
	config := fmt.Sprintf("Port %d\nListenAddress 127.0.0.1\nHostKey %s\nPidFile %s\nAuthorizedKeysFile %s\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nUsePAM no\nStrictModes no\nAllowUsers %s\nLogLevel VERBOSE\n", port, filepath.Join(directory, "host"), filepath.Join(directory, "pid"), filepath.Join(directory, "identity.pub"), current.Username)
	configPath := filepath.Join(directory, "sshd_config")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	var diagnostics boundedOutput
	daemon := exec.Command(sshd, "-D", "-e", "-f", configPath)
	daemon.Stderr = &diagnostics
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { daemon.Process.Kill(); daemon.Wait() })
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 50*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sshd fixture did not listen: %s", diagnostics.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	key, err := os.ReadFile(filepath.Join(directory, "host.pub"))
	if err != nil {
		t.Fatal(err)
	}
	known := filepath.Join(directory, "known_hosts")
	if err := os.WriteFile(known, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	sshConfig := filepath.Join(directory, "config")
	config = fmt.Sprintf("Host pencode-loopback\n HostName 127.0.0.1\n User %s\n Port %d\n IdentityFile %s\n IdentitiesOnly yes\n IdentityAgent none\n UserKnownHostsFile %s\n GlobalKnownHostsFile /dev/null\n", current.Username, port, filepath.Join(directory, "identity"), known)
	if err := os.WriteFile(sshConfig, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	target := Target{Host: "pencode-loopback", ConfigFile: sshConfig, ServerBinary: binary, ServerRoot: filepath.Join(directory, "remote's servers")}
	connector := Connector{Output: &diagnostics}
	fingerprintOutput, err := exec.Command("ssh-keygen", "-l", "-E", "sha256", "-f", filepath.Join(directory, "host.pub")).Output()
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := strings.Fields(string(fingerprintOutput))[1]
	var prompts, confirmations atomic.Int32
	// Rejecting an unknown host must never reach password authentication or
	// modify known_hosts. This is the state of a first connection in the GUI.
	rejectCtx := WithPrompt(t.Context(), func(ctx context.Context, prompt AuthenticationPrompt) (string, error) {
		confirmations.Add(1)
		if !prompt.Confirm || !strings.Contains(prompt.Text, fingerprint) {
			t.Error("unknown-host prompt did not expose its fingerprint")
		}
		return "no", nil
	})
	if stream, err := connector.Connect(rejectCtx, target, nil); err == nil {
		stream.Close()
		t.Fatal("rejected host was connected")
	}
	if content, err := os.ReadFile(known); err != nil || len(content) != 0 || confirmations.Load() != 1 {
		t.Fatal("rejecting an unknown host recorded trust", err)
	}
	confirmations.Store(0)
	first := true
	connect := func() (*client.Client, []string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		var progress []string
		ctx = WithPrompt(ctx, func(ctx context.Context, prompt AuthenticationPrompt) (string, error) {
			if prompt.Confirm {
				confirmations.Add(1)
				if !strings.Contains(prompt.Text, fingerprint) {
					t.Error("host confirmation did not expose the expected fingerprint")
					return "no", nil
				}
				return "yes", nil
			}
			prompts.Add(1)
			return "integration-passphrase", nil
		})
		before := prompts.Load()
		stream, err := connector.Connect(ctx, target, func(message string) { progress = append(progress, message) })
		if err != nil {
			t.Fatalf("connect: %v\n%s", err, diagnostics.String())
		}
		wantConfirmations := int32(0)
		if first {
			wantConfirmations = 1
		}
		if confirmations.Load() != wantConfirmations {
			t.Fatalf("host confirmation count = %d, want %d", confirmations.Load(), wantConfirmations)
		}
		confirmations.Store(0)
		first = false
		if prompts.Load() != before+1 {
			t.Fatalf("each SSH connection must authenticate once, got %d prompts", prompts.Load()-before)
		}
		backend, err := client.New(ctx, stream)
		if err != nil {
			t.Fatalf("handshake: %v\n%s", err, diagnostics.String())
		}
		t.Cleanup(func() { backend.Close() })
		return backend, progress
	}
	backend, progress := connect()
	if content, err := os.ReadFile(known); err != nil || !strings.Contains(string(content), strings.Fields(string(key))[1]) {
		t.Fatal("approved host key was not recorded", err)
	}
	if !strings.Contains(strings.Join(progress, ""), "上传") {
		t.Fatal("first connect did not install the server")
	}
	workspaceDirectory := filepath.Join(directory, "workspace")
	if err := os.Mkdir(workspaceDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDirectory, "main.go"), []byte("package original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := backend.OpenWorkspace(t.Context(), workspaceDirectory)
	if err != nil {
		t.Fatal(err)
	}
	document, _, err := backend.OpenDocument(t.Context(), w.ID, "main.go", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := document.Save(t.Context(), "package ssh\n"); err != nil {
		t.Fatal(err)
	}
	document.Close()
	process, err := backend.OpenProcess(t.Context(), w.Path, "sh", "-c", "printf 'remote process'; printf 'remote log' >&2")
	if err != nil {
		t.Fatal(err)
	}
	logs := make(chan []byte, 1)
	go func() { data, _ := io.ReadAll(process.Stderr()); logs <- data }()
	data, err := io.ReadAll(process)
	if err != nil || string(data) != "remote process" || string(<-logs) != "remote log" {
		t.Fatal("multiplexed process streams failed", string(data), err)
	}
	process.Close()
	pty, err := backend.OpenTerminal(t.Context(), w.Path, 80, 12)
	if err != nil {
		t.Fatal(err)
	}
	defer pty.Close()
	if _, err := pty.Write([]byte("printf '\\120\\105\\116_SSH_PTY\\n'; exit\n")); err != nil {
		t.Fatal(err)
	}
	data, err = io.ReadAll(pty)
	if err != nil || !strings.Contains(string(data), "PEN_SSH_PTY") {
		t.Fatal("remote PTY shell failed", string(data), err)
	}
	pty.Close()
	backend.Close()
	backend, progress = connect()
	if strings.Contains(strings.Join(progress, ""), "上传") {
		t.Fatal("reconnect uploaded an existing matching binary")
	}
	backend.Close()
	entries, err := os.ReadDir(target.ServerRoot)
	if err != nil || len(entries) != 1 || entries[0].Name() != "pencode-server" {
		t.Fatal("server publication left staging directories", entries, err)
	}
	data, err = os.ReadFile(filepath.Join(workspaceDirectory, "main.go"))
	if err != nil || string(data) != "package ssh\n" {
		t.Fatal("SSH save did not reach the server filesystem", err)
	}
	// A previously trusted key changing is a hard failure, never another
	// trust prompt or a password request.
	otherKey, err := os.ReadFile(filepath.Join(directory, "identity.pub"))
	if err != nil {
		t.Fatal(err)
	}
	mismatch := []byte(fmt.Sprintf("[127.0.0.1]:%d %s", port, otherKey))
	if err := os.WriteFile(known, mismatch, 0o600); err != nil {
		t.Fatal(err)
	}
	var changedPrompts atomic.Int32
	changedCtx := WithPrompt(t.Context(), func(ctx context.Context, prompt AuthenticationPrompt) (string, error) {
		changedPrompts.Add(1)
		return "yes", nil
	})
	if stream, err := connector.Connect(changedCtx, target, nil); err == nil {
		stream.Close()
		t.Fatal("changed host key was accepted")
	}
	if content, err := os.ReadFile(known); err != nil || string(content) != string(mismatch) || changedPrompts.Load() != 0 {
		t.Fatal("changed host key prompted or rewrote trust", err)
	}

}
