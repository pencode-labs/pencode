package connection

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pencode-labs/pencode/remote"
)

func TestProfilesPublicationAndInvalidData(t *testing.T) {
	name := filepath.Join(t.TempDir(), "resources", "connection.json")
	profiles := []Profile{
		{Name: "开发机", SSH: remote.Target{Host: "devbox", Port: 2222, IdentityFile: "/keys/development"}, Directory: "~/中文 project"},
		{Name: "构建机", SSH: remote.Target{Host: "builder"}},
	}
	if err := SaveProfiles(name, profiles); err != nil {
		t.Fatal(err)
	}
	got := LoadProfiles(name)
	if len(got) != len(profiles) || got[0] != profiles[0] || got[1] != profiles[1] {
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
	if len(got) != len(profiles) || got[0] != profiles[0] || got[1] != profiles[1] {
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
