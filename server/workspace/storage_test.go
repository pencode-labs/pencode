package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/pencode-labs/pencode/protocol"
)

func TestStorageMeasuresHostRootsAndCleansOnlySelectedData(t *testing.T) {
	base := t.TempDir()
	for _, env := range []string{"HOME", "USERPROFILE"} {
		t.Setenv(env, base)
	}
	for _, env := range []string{"XDG_CONFIG_HOME", "APPDATA"} {
		t.Setenv(env, filepath.Join(base, "config"))
	}
	for _, env := range []string{"XDG_CACHE_HOME", "LOCALAPPDATA"} {
		t.Setenv(env, filepath.Join(base, "cache"))
	}
	profiles := filepath.Join(base, "shared-profiles")
	t.Setenv("PENCODE_PROFILE_DIR", profiles)
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	config, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	resources, exts := filepath.Join(config, "pencode", "resources"), filepath.Join(base, "custom-extensions")
	put := func(root, name, content string) string {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	const finished = "20261125T080752Z-gui-12-345"
	const running = "20261125T080752Z-server-34-567"
	put(profiles, finished+"/session.json", "{}")
	put(profiles, finished+"/resources-final.pprof", "abcd")
	active := put(profiles, running+"/session.json", "{}")
	unrelated := put(profiles, "other/document", "safe")
	cacheFile := put(cache, "pencode/ripgrep/stale/rg", "123")
	extension := put(exts, "my-extension/bin/tool", "12345")
	settings := put(resources, "settings.json", "{}")
	tool := put(resources, "debug-adapters/managed/tool", "1234567")
	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Dir(unrelated), filepath.Join(filepath.Dir(cacheFile), "outside")); err != nil {
			t.Fatal(err)
		}
	}
	run := func(mask protocol.StorageMask) protocol.StorageReport {
		t.Helper()
		report, err := DiskStorage(t.Context(), resources, exts, mask)
		if err != nil || report.Problem != "" {
			t.Fatal(err, report.Problem)
		}
		return report
	}
	r := run(0)
	for k, want := range map[protocol.StorageKind]uint64{protocol.StorageProfiles: 6, protocol.StorageOtherProfiles: 2, protocol.StorageExtensions: 5, protocol.StorageOther: 2, protocol.StorageTools: 7, protocol.StorageSearchCache: 3} {
		if r.Sizes[k].Bytes != want {
			t.Fatalf("category %d = %+v, want %d", k, r.Sizes[k], want)
		}
	}
	mask := protocol.StorageSearchCache.Mask() | protocol.StorageProfiles.Mask()
	r = run(mask)
	if r.Removed != (protocol.StorageSize{Files: 3, Bytes: 9}) || r.Sizes[protocol.StorageProfiles].Files != 0 || r.Sizes[protocol.StorageSearchCache].Files != 0 {
		t.Fatal(r)
	}
	if r := run(mask); r.Removed.Files != 0 {
		t.Fatal("repeated cleanup removed more files", r)
	}
	for _, path := range []string{active, unrelated, extension, settings, tool} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := DiskStorage(t.Context(), resources, exts, protocol.StorageExtensions.Mask()); !errors.Is(err, protocol.ErrProtocol) {
		t.Fatal("accepted read-only category", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := DiskStorage(ctx, resources, exts, mask); !errors.Is(err, context.Canceled) {
		t.Fatal("ignored cancellation", err)
	}
}
