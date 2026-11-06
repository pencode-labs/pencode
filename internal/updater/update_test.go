package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
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
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
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

func testArchive(t *testing.T, format, name string) []byte {
	t.Helper()
	var b bytes.Buffer
	files := map[string]string{name: "binary", "pencode/" + VersionFile: "v1.1.0\n", "pencode/resources/catalog.bin": "resources"}
	if format == ".zip" {
		w := zip.NewWriter(&b)
		for name, data := range files {
			h := &zip.FileHeader{Name: name}
			h.SetMode(0o755)
			f, err := w.CreateHeader(h)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(f, data); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		g := gzip.NewWriter(&b)
		w := tar.NewWriter(g)
		for name, data := range files {
			if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data))}); err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(w, data); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if err := g.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return b.Bytes()
}

func TestStageVerifiesAndPreservesInstallation(t *testing.T) {
	for _, format := range []string{".zip", ".tar.gz"} {
		t.Run(format, func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, "installed")
			if err := os.Mkdir(root, 0o755); err != nil {
				t.Fatal(err)
			}
			exe := filepath.Join(root, "pencode")
			if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
				t.Fatal(err)
			}
			data := testArchive(t, format, "pencode/pencode")
			digest := sha256.Sum256(data)
			name := "package" + format
			bad := false
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/sums" {
					fmt.Fprintf(w, "%x  %s\n", digest, name)
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
			if b, err := os.ReadFile(filepath.Join(stage, "pencode/resources/catalog.bin")); err != nil || string(b) != "resources" {
				t.Fatalf("resources: %q %v", b, err)
			}
			if b, _ := os.ReadFile(exe); string(b) != "old" {
				t.Fatal("running installation changed")
			}
			bad = true
			stage, err = c.Stage(context.Background(), root, exe, r)
			if err == nil || !strings.Contains(err.Error(), "SHA-256") {
				t.Fatal("corrupt download accepted", err)
			}
			if _, err := os.Stat(stage); !os.IsNotExist(err) {
				t.Fatal("failed staging not removed", err)
			}
		})
	}
}

func TestArchiveRejectsTraversal(t *testing.T) {
	for _, format := range []string{".zip", ".tar.gz"} {
		for _, name := range []string{"../escape", "pencode/../../escape", "pencode/../escape", "pencode/C:/escape", "/pencode/escape"} {
			t.Run(format+name, func(t *testing.T) {
				dir := t.TempDir()
				file := filepath.Join(dir, "test"+format)
				if err := os.WriteFile(file, testArchive(t, format, name), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := extract(file, dir); err == nil {
					t.Fatal("unsafe path accepted")
				}
			})
		}
	}
}
