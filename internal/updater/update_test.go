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

	"golang.org/x/mod/semver"
)

func TestInstallationFindsVersion(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "pencode")
	metadata := root
	if runtime.GOOS == "darwin" {
		root = filepath.Join(root, "pencode.app", "Contents")
		binary = filepath.Join(root, "MacOS", "pencode")
		metadata = filepath.Join(root, "Resources")
	}
	for _, directory := range []string{filepath.Dir(binary), metadata} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(binary, []byte("executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metadata, VersionFile), []byte("v1.2.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// macOS temporary directories may themselves be symlinks.
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	got, version, err := Installation(binary)
	if err != nil || got != root || version != "v1.2.3" {
		t.Fatalf("installation = %q, %q, %v; want %q, v1.2.3", got, version, err, root)
	}
	resolved, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	if relative, err := filepath.Rel(got, resolved); err != nil || !filepath.IsLocal(relative) {
		t.Fatalf("executable is outside installation: %s, %v", relative, err)
	}
}

func TestVersionOrder(t *testing.T) {
	for _, v := range []string{"v1.2.3", "1.2.3", "v2026.11.6", "v1.2.3-rc.1"} {
		if !semver.IsValid(version(v)) {
			t.Fatal(v)
		}
	}
	for _, v := range []string{"dev", "v1.x.0", "v1..0"} {
		if semver.IsValid(version(v)) {
			t.Fatal(v)
		}
	}
	if version("v1.0.0") != version("1.0.0") {
		t.Fatal("version prefix was not normalized")
	}
}

func TestCheckPublicReleases(t *testing.T) {
	ext := installerExtension(runtime.GOOS)
	installer := func(tag string) string {
		return "pencode-" + tag + "-" + runtime.GOOS + "-" + runtime.GOARCH + ext
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := "http://" + r.Host
		fragment := releasesPath + "/expanded_assets/"
		if r.Header.Get("X-GitHub-Api-Version") != "" || strings.Contains(r.Header.Get("Accept"), "github+json") {
			t.Errorf("unexpected API headers on %s", r.URL)
		}
		switch r.URL.Path {
		case releasesPath:
			switch r.URL.RawQuery {
			case "":
				for _, tag := range []string{"v1.9.0", "v1.11.0-rc.1", "v2.0.0", "v2.1.0", "v2.2.0"} {
					fmt.Fprintf(w, `<include-fragment src=%q></include-fragment>`, origin+fragment+tag)
				}
				fmt.Fprintf(w, `<a rel="next" href=%q>Next</a>`, releasesPath+"?page=2")
			case "page=2":
				fmt.Fprintf(w, `<include-fragment src=%q></include-fragment>`, fragment+"v1.10.0")
			default:
				t.Errorf("unexpected page %s", r.URL)
				http.NotFound(w, r)
			}
		case fragment + "v1.10.0", fragment + "v1.11.0-rc.1":
			tag := strings.TrimPrefix(r.URL.Path, fragment)
			path := releasesPath + "/download/" + tag + "/"
			fmt.Fprintf(w, `<a href=%q>Installer</a><a href=%q>Checksums</a>`, path+installer(tag), origin+path+"SHA256SUMS")
		case fragment + "v2.0.0":
			http.NotFound(w, r)
		case fragment + "v2.1.0":
			fmt.Fprintf(w, `<a href=%q>Installer without checksums</a>`, releasesPath+"/download/v2.1.0/"+installer("v2.1.0"))
		case fragment + "v2.2.0":
			path := releasesPath + "/download/v2.2.0/"
			fmt.Fprintf(w, `<a href=%q>Installer on another host</a><a href=%q>Checksums</a>`, "https://example.test"+path+installer("v2.2.0"), path+"SHA256SUMS")
		default:
			t.Errorf("unexpected request %s", r.URL)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer s.Close()
	c := Client{HTTP: s.Client(), GitHub: s.URL}
	for current, tag := range map[string]string{
		"1.9.1":        "v1.10.0",
		"v1.10.0-rc.1": "v1.11.0-rc.1",
		"v2.0.0":       "",
	} {
		want := Release{}
		if tag != "" {
			name := installer(tag)
			path := s.URL + releasesPath + "/download/" + tag + "/"
			want = Release{Tag: tag, Name: name, URL: path + name, Checksums: path + "SHA256SUMS"}
		}
		if got, err := c.Check(context.Background(), current); err != nil || got != want {
			t.Errorf("Check(%q) = %+v, %v; want %+v", current, got, err, want)
		}
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
