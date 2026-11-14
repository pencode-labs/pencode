package connection

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/pencode-labs/pencode/internal/updater"
	"github.com/pencode-labs/pencode/remote"
)

func TestDefaultDialerUsesInstalledVersionForSSH(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "pencode")
	if runtime.GOOS == "darwin" {
		executable = filepath.Join(root, "Pencode.app", "Contents", "MacOS", "Pencode")
	}
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	versionPath := filepath.Join(root, updater.VersionFile)
	if runtime.GOOS == "darwin" {
		versionPath = filepath.Join(root, "Resources", updater.VersionFile)
		if err := os.MkdirAll(filepath.Dir(versionPath), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(versionPath, []byte("v1.2.3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dialer := DefaultDialer(executable, nil)
	if dialer.SSH.Version != "v1.2.3" || dialer.SSH.ServerDirectory != "" {
		t.Fatal("installed client must download its exact release", dialer.SSH)
	}
}

func TestProfilesPublicationAndInvalidData(t *testing.T) {
	name := filepath.Join(t.TempDir(), "resources", "connection.json")
	profiles := []Profile{
		{Name: "开发机", SSH: remote.Target{Host: "devbox", Port: 2222, IdentityFile: "/keys/development"}, Directory: "~/中文 project"},
		{Name: "构建机", SSH: remote.Target{Host: "builder"}},
		{Name: "直连", WebSocket: remote.WebSocketTarget{Address: "wss://dev.example/ws", Token: "secret"}},
	}
	if err := SaveProfiles(name, profiles); err != nil {
		t.Fatal(err)
	}
	got := LoadProfiles(name)
	if !slices.Equal(got, profiles) {
		t.Fatal("profiles did not round trip", got)
	}
	if info, err := os.Stat(name); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatal("connection profile permissions", info, err)
	}
	oversized := append([]Profile(nil), profiles...)
	oversized[0].Directory = strings.Repeat("a", 64<<10)
	if err := SaveProfiles(name, oversized); err == nil {
		t.Fatal("oversized publication was accepted")
	}
	got = LoadProfiles(name)
	if !slices.Equal(got, profiles) {
		t.Fatal("rejected publication replaced the prior profiles", got)
	}
	for _, data := range []string{`{"connections":[{"ssh":{"host":"partial"}}`, strings.Repeat(" ", (64<<10)+1)} {
		if err := os.WriteFile(name, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := LoadProfiles(name); len(got) != 0 {
			t.Fatal("invalid data returned partially decoded settings", got)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(name))
	if err != nil || len(entries) != 1 {
		t.Fatal("publication leaked temporary files", entries, err)
	}
}
