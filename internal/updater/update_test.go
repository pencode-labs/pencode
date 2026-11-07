package updater

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestVersionOrder(t *testing.T) {
	for _, v := range []string{"v1.2.3", "1.2.3", "v2026.11.6", "v1.2.3-rc.1"} {
		if !validVersion(v) {
			t.Fatal(v)
		}
	}
	for _, v := range []string{"dev", "v1.x.0", "v1..0"} {
		if validVersion(v) {
			t.Fatal(v)
		}
	}
	if compareVersion("v1.10.0", "v1.9.9") <= 0 || compareVersion("v1.0.0", "1.0.0") != 0 {
		t.Fatal("incorrect version order")
	}
}

func TestCheckTagsAndPlatformAssets(t *testing.T) {
	ext := installerExtension(runtime.GOOS)
	name := "pencode-v1.10.0-" + runtime.GOOS + "-" + runtime.GOARCH + ext
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tags":
			io.WriteString(w, `[{"name":"v1.9.0"},{"name":"v1.11.0-rc.1"},{"name":"v1.10.0"},{"name":"v2.0.0"}]`)
		case "/releases/tags/v1.10.0":
			fmt.Fprintf(w, `{"assets":[{"name":%q,"browser_download_url":"https://example.test/package"},{"name":"SHA256SUMS","browser_download_url":"https://example.test/sums"}]}`, name)
		case "/releases/tags/v2.0.0":
			http.NotFound(w, r)
		default:
			t.Errorf("unexpected request %s", r.URL)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer s.Close()
	c := Client{HTTP: s.Client(), API: s.URL}
	r, err := c.Check(context.Background(), "v1.9.1")
	if err != nil || r.Tag != "v1.10.0" || r.Name != name {
		t.Fatalf("%+v %v", r, err)
	}
	r, err = c.Check(context.Background(), "v2.0.0")
	if err != nil || r.Tag != "" {
		t.Fatalf("downgrade offered: %+v %v", r, err)
	}
}

func TestStageVerifiesAndPreservesInstallation(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "installed")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "pencode")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte("native installer payload")
	digest := sha256.Sum256(data)
	name := "package" + installerExtension(runtime.GOOS)
	bad, missingChecksum := false, false
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sums" {
			if !missingChecksum {
				fmt.Fprintf(w, "%x  %s\n", digest, name)
			}
			return
		}
		if bad {
			io.WriteString(w, "corrupt")
			return
		}
		w.Write(data)
	}))
	defer s.Close()
	c := Client{HTTP: s.Client()}
	r := Release{Tag: "v1.1.0", Name: name, URL: s.URL + "/package", Checksums: s.URL + "/sums"}
	stage, err := c.Stage(context.Background(), root, exe, r)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stage)
	if b, err := os.ReadFile(filepath.Join(stage, name)); err != nil || string(b) != string(data) {
		t.Fatalf("installer: %q %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(stage, "installer")); err != nil || string(b) != name {
		t.Fatalf("installer manifest: %q %v", b, err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "old" {
		t.Fatal("running installation changed")
	}
	for _, failure := range []string{"corrupt", "missing-checksum"} {
		bad, missingChecksum = failure == "corrupt", failure == "missing-checksum"
		failedStage, err := c.Stage(context.Background(), root, exe, r)
		if err == nil || !strings.Contains(err.Error(), "SHA-256") {
			t.Fatal("invalid download accepted", err)
		}
		if _, err := os.Stat(failedStage); !os.IsNotExist(err) {
			t.Fatal("failed staging not removed", err)
		}
	}
}

func TestStageRejectsInvalidInstallerPaths(t *testing.T) {
	for _, name := range []string{"../escape.exe", "/escape.exe", "sub/package.exe", "package.zip", "package.tar.gz"} {
		c := Client{}
		if _, err := c.Stage(context.Background(), "root", "root/pencode", Release{Name: name}); err == nil {
			t.Fatalf("invalid installer name accepted: %q", name)
		}
	}
}
