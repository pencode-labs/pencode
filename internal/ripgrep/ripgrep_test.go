//go:build onix_release || pencode_ripgrep

package ripgrep

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBundledSearch(t *testing.T) {
	cache := t.TempDir()
	switch runtime.GOOS {
	case "darwin":
		t.Setenv("HOME", cache)
	case "windows":
		t.Setenv("LocalAppData", cache)
	default:
		t.Setenv("XDG_CACHE_HOME", cache)
	}
	t.Setenv("PATH", t.TempDir())
	t.Setenv("RIPGREP_CONFIG_PATH", filepath.Join(cache, "no-such-config"))
	search := func(want string) {
		t.Helper()
		var result string
		err := Run(t.Context(), "", strings.NewReader("first\nneedle\nlast"), []string{"-F", "-e", want, "-"}, func(r *bufio.Reader) error {
			data, err := r.ReadString('\n')
			result = data
			if data == "" && want == "absent" {
				return nil
			}
			return err
		})
		if err != nil || want == "needle" && result != "needle\n" || want == "absent" && result != "" {
			t.Fatalf("query %q: result %q, error %v", want, result, err)
		}
	}
	search("needle")
	name, err := executable()
	if err != nil || !validBinary(name) || !strings.HasPrefix(name, cache+string(os.PathSeparator)) {
		t.Fatalf("bundled executable %q: %v", name, err)
	}
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	search("absent")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := Run(ctx, "", nil, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
