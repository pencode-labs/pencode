package extensions

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func marketArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
	for name, body := range files {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(0o755)
		f, err := writer.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(f, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestMarketplaceReleaseAndPlatform(t *testing.T) {
	var downloads atomic.Int32
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/extensions.json":
			io.WriteString(w, `{"extensions":[{"name":"test","repository":"https://github.com/pencode-labs/test","description":"Test"},{"name":"other","repository":"https://github.com/pencode-labs/other"}]}`)
		case "/repos/pencode-labs/test/releases/latest":
			fmt.Fprintf(w, `{"tag_name":"v1.10.0","assets":[{"name":"test-v1.10.0-linux-arm64.zip","browser_download_url":%q,"digest":"sha256:example"},{"name":"test-v1.10.0-linux-amd64.zip","browser_download_url":"https://example.com/wrong.zip"}]}`, server.URL+"/package.zip")
		case "/package.zip":
			downloads.Add(1)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	m := Marketplace{Source: server.URL + "/extensions.json", API: server.URL, Client: server.Client()}
	items, err := m.List(t.Context(), "linux", "arm64")
	if err != nil || len(items) != 2 {
		t.Fatal(items, err)
	}
	if items[0].Version != "1.10.0" || items[0].URL != server.URL+"/package.zip" || items[0].Problem != "" || items[1].Problem == "" {
		t.Fatal(items)
	}
	for version, want := range map[string]bool{"1.9.0": true, "v1.10.0": false, "1.11.0": false, "1.10.0-beta.1": true, "invalid": false} {
		if got := items[0].NewerThan(version); got != want {
			t.Errorf("NewerThan(%s) = %v", version, got)
		}
	}
	items, err = m.List(t.Context(), "windows", "arm64")
	if err != nil || items[0].URL != "" || !strings.Contains(items[0].Problem, "windows-arm64") {
		t.Fatal(items, err)
	}
	if downloads.Load() != 0 {
		t.Fatal("checking release metadata downloaded a package")
	}
}

func TestMarketplaceReleaseCheckFailureIsNotDownloadFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/extensions.json" {
			io.WriteString(w, `{"extensions":[{"name":"test","repository":"https://github.com/pencode-labs/test"}]}`)
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	items, err := (Marketplace{Source: server.URL + "/extensions.json", API: server.URL}).List(t.Context(), "linux", "amd64")
	if err != nil || len(items) != 1 {
		t.Fatal(items, err)
	}
	if !strings.Contains(items[0].Problem, "HTTP 403") || strings.Contains(items[0].Problem, "下载") || items[0].URL != "" {
		t.Fatal(items[0])
	}
}

func TestMarketplaceArchiveInstallAndUpdate(t *testing.T) {
	files := map[string]string{
		"test/resources/extension.json": `{"id":"test","version":"1.0.0","entry":"bin/test","disabled":true}`,
		"test/bin/test":                 "disabled fixture",
	}
	archive := marketArchive(t, files)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(archive) }))
	defer server.Close()
	m := Marketplace{Client: server.Client()}
	listing := Listing{Name: "test", Version: "1.0.0", URL: server.URL}
	digest := sha256.Sum256(archive)
	listing.Digest = "sha256:" + hex.EncodeToString(digest[:])
	target := t.TempDir()
	backend := extensionBackend(t, target)
	item, bundle, err := m.Install(t.Context(), backend, listing, false)
	if err != nil || bundle != nil || item.Version != "1.0.0" || !item.Disabled {
		t.Fatal(item, err)
	}
	files["test/resources/extension.json"] = `{"id":"test","version":"1.1.0","entry":"bin/test","custom":"preserved"}`
	archive = marketArchive(t, files)
	listing.Version, listing.Digest = "1.1.0", ""
	item, bundle, err = m.Install(t.Context(), backend, listing, true)
	if err != nil || bundle != nil || item.Version != "1.1.0" || !item.Disabled {
		t.Fatal(item, err)
	}
	data, err := os.ReadFile(filepath.Join(item.Path, "resources/extension.json"))
	if err != nil || !bytes.Contains(data, []byte(`"custom": "preserved"`)) {
		t.Fatal(string(data), err)
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 1 || entries[0].Name() != "test" {
		t.Fatal(entries, err)
	}
	info, err := os.Stat(filepath.Join(item.Path, "bin/test"))
	if err != nil || runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		t.Fatal(info, err)
	}
}

func TestMarketplaceRejectsInvalidArchives(t *testing.T) {
	manifest := `{"id":"test","version":"1.0.0","entry":"bin/test","disabled":true}`
	for _, test := range []struct{ name, path, body, digest string }{
		{name: "traversal", path: "test/../../escape", body: "bad"},
		{name: "absolute", path: "/escape", body: "bad"},
		{name: "backslash", path: `test/..\escape`, body: "bad"},
		{name: "wrong-root", path: "other/file", body: "bad"},
		{name: "wrong-id", path: "test/resources/extension.json", body: `{"id":"other","version":"1.0.0"}`},
		{name: "wrong-version", path: "test/resources/extension.json", body: `{"id":"test","version":"0.9.0"}`},
		{name: "checksum", digest: "sha256:wrong"},
	} {
		t.Run(test.name, func(t *testing.T) {
			files := map[string]string{"test/resources/extension.json": manifest}
			if test.path != "" {
				files[test.path] = test.body
			}
			archive := marketArchive(t, files)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(archive) }))
			defer server.Close()
			m := Marketplace{Client: server.Client()}
			_, err := m.download(t.Context(), t.TempDir(), Listing{Name: "test", Version: "1.0.0", URL: server.URL, Digest: test.digest})
			if err == nil {
				t.Fatal("accepted invalid archive")
			}
		})
	}
	t.Run("symlink", func(t *testing.T) {
		var data bytes.Buffer
		writer := zip.NewWriter(&data)
		h := &zip.FileHeader{Name: "test/link"}
		h.SetMode(fs.ModeSymlink | 0o777)
		f, err := writer.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(f, "../../escape")
		writer.Close()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data.Bytes()) }))
		defer server.Close()
		if _, err := (Marketplace{}).download(t.Context(), t.TempDir(), Listing{Name: "test", Version: "1.0.0", URL: server.URL}); err == nil {
			t.Fatal("accepted symlink")
		}
	})
}

func TestMarketplaceRejectsInvalidCatalog(t *testing.T) {
	for _, names := range [][]string{{"../bad"}, {"duplicate", "duplicate"}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			items := make([]Listing, len(names))
			for i, name := range names {
				items[i].Name = name
			}
			json.NewEncoder(w).Encode(map[string]any{"extensions": items})
		}))
		_, err := (Marketplace{Source: server.URL}).List(t.Context(), "linux", "amd64")
		server.Close()
		if err == nil {
			t.Fatal("accepted invalid catalog", names)
		}
	}
}
