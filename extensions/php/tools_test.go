package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func writeFile(t *testing.T, name, data string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(data), mode); err != nil {
		t.Fatal(err)
	}
}

func zipData(t *testing.T, name, data string, mode os.FileMode) []byte {
	t.Helper()
	var buffer bytes.Buffer
	w := zip.NewWriter(&buffer)
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetMode(mode)
	file, err := w.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(file, data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func tarData(t *testing.T, name, data string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	w := tar.NewWriter(gz)
	if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestGitHubInstallsAndOfflineCache(t *testing.T) {
	cleanEnvironment(t)
	bin := t.TempDir()
	for _, name := range []string{"node", "php"} {
		writeFile(t, filepath.Join(bin, toolFilename(name)), "runtime", 0o755)
	}
	t.Setenv("PATH", bin)
	for _, name := range []string{"phpactor", "phpantom", "xdebug"} {
		t.Run(name, func(t *testing.T) {
			asset, payload := "phpactor.phar", []byte("<?php echo 'server';")
			switch name {
			case "phpantom":
				var err error
				asset, err = phpantomAsset(runtime.GOOS, runtime.GOARCH)
				if err != nil {
					t.Skip(err)
				}
				if runtime.GOOS == "windows" {
					payload = zipData(t, "phpantom_lsp.exe", "server", 0o755)
				} else {
					payload = tarData(t, "./phpantom_lsp", "server")
				}
			case "xdebug":
				asset = "php-debug-1.2.3.vsix"
				payload = zipData(t, "extension/out/phpDebug.js", "console.log('adapter')", 0o644)
			}
			downloads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/releases/latest") {
					json.NewEncoder(w).Encode(map[string]any{"tag_name": "v1.2.3", "assets": []map[string]string{{"name": asset, "browser_download_url": "https://example.test/asset"}}})
					return
				}
				downloads++
				w.Write(payload)
			}))
			defer server.Close()
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				r.URL.Scheme, r.URL.Host = "http", strings.TrimPrefix(server.URL, "http://")
				return http.DefaultTransport.RoundTrip(r)
			})}
			tools := &toolManager{directory: filepath.Join(t.TempDir(), "cache with spaces"), client: client, log: io.Discard}
			command, err := tools.ensure(t.Context(), name)
			if err != nil || command.path == "" {
				t.Fatal(command, err)
			}
			if name == "phpactor" && (len(command.args) != 2 || command.args[1] != "language-server") {
				t.Fatal(command)
			}
			if name == "xdebug" && len(command.args) != 1 {
				t.Fatal("adapter must use stdio without --server", command)
			}
			server.Close()
			if _, err := tools.ensure(t.Context(), name); err != nil || downloads != 1 {
				t.Fatal("cached launch accessed network", downloads, err)
			}
			entries, err := os.ReadDir(tools.directory)
			if err != nil || len(entries) != 1 || entries[0].Name() != name {
				t.Fatal("staging directory leaked", entries, err)
			}
		})
	}
}

func TestPathTakesPriorityAndUnknownToolsDoNotInstall(t *testing.T) {
	cleanEnvironment(t)
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	tools := &toolManager{directory: filepath.Join(t.TempDir(), "absent"), log: io.Discard}
	for name, binary := range map[string]string{"intelephense": "intelephense", "phptools": "phptools", "phpactor": "phpactor", "phpantom": "phpantom_lsp"} {
		file := filepath.Join(bin, toolFilename(binary))
		writeFile(t, file, "server", 0o755)
		command, err := tools.ensure(t.Context(), name)
		if err != nil || command.path != file {
			t.Fatal(command, err)
		}
	}
	if _, err := tools.ensure(t.Context(), "unknown"); err == nil {
		t.Fatal("accepted unknown tool")
	}
	if _, err := os.Stat(tools.directory); !os.IsNotExist(err) {
		t.Fatal("PATH launch touched cache", err)
	}
}

func TestFailedDownloadDoesNotPublishCache(t *testing.T) {
	cleanEnvironment(t)
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	writeFile(t, filepath.Join(bin, toolFilename("php")), "runtime", 0o755)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Status: "503 Service Unavailable", Body: io.NopCloser(strings.NewReader("unavailable"))}, nil
	})}
	tools := &toolManager{directory: t.TempDir(), client: client, log: io.Discard}
	if _, err := tools.ensure(t.Context(), "phpactor"); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatal("download error missing", err)
	}
	entries, err := os.ReadDir(tools.directory)
	if err != nil || len(entries) != 0 {
		t.Fatal("failed download left files", entries, err)
	}
}

func TestArchiveRejectsTraversalAndLinks(t *testing.T) {
	for _, entry := range []struct {
		name string
		mode os.FileMode
	}{{"../outside", 0o644}, {"extension/../../outside", 0o644}, {"/absolute", 0o644}, {`extension\outside`, 0o644}, {"extension/link", os.ModeSymlink | 0o777}} {
		t.Run(entry.name, func(t *testing.T) {
			archive := filepath.Join(t.TempDir(), "test.zip")
			if err := os.WriteFile(archive, zipData(t, entry.name, "bad", entry.mode), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := unpack(archive, t.TempDir(), "xdebug", "linux"); err == nil {
				t.Fatal("accepted unsafe entry")
			}
		})
	}
}

func TestNPMInstallAndCache(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell")
	}
	cleanEnvironment(t)
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	writeFile(t, filepath.Join(bin, "npm"), "npm JS placeholder", 0o644)
	// LookPath requires npm itself to be executable.
	if err := os.Chmod(filepath.Join(bin, "npm"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"intelephense", "phptools"} {
		t.Run(name, func(t *testing.T) {
			file := "node_modules/intelephense/lib/intelephense.js"
			if name == "phptools" {
				var err error
				file, err = phpToolsPath(runtime.GOOS, runtime.GOARCH)
				if err != nil {
					t.Skip(err)
				}
			}
			script := fmt.Sprintf("#!/bin/sh\n[ \"$2\" = install ] || exit 1\n[ \"$3\" = --prefix ] || exit 2\n/bin/mkdir -p \"$4/%s\"\nprintf server > \"$4/%s\"\n/bin/chmod +x \"$4/%s\"\n", filepath.Dir(file), file, file)
			writeFile(t, filepath.Join(bin, "node"), script, 0o755)
			tools := &toolManager{directory: filepath.Join(t.TempDir(), "cache"), log: io.Discard}
			command, err := tools.ensure(context.Background(), name)
			if err != nil || command.path == "" {
				t.Fatal(command, err)
			}
			writeFile(t, filepath.Join(bin, "node"), "#!/bin/sh\nexit 99\n", 0o755)
			if _, err := tools.ensure(t.Context(), name); err != nil {
				t.Fatal("cache attempted to reinstall", err)
			}
		})
	}
}

func TestPlatformAssets(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			if _, err := phpToolsPath(goos, arch); err != nil {
				t.Fatal(err)
			}
			asset, err := phpantomAsset(goos, arch)
			if err != nil || !strings.HasPrefix(asset, "phpantom_lsp-") || (goos == "windows") != strings.HasSuffix(asset, ".zip") {
				t.Fatal(asset, err)
			}
		}
	}
	if _, err := phpToolsPath("linux", "386"); err == nil {
		t.Fatal("accepted unsupported architecture")
	}
	if _, err := phpantomAsset("plan9", "amd64"); err == nil {
		t.Fatal("accepted unsupported OS")
	}
}
