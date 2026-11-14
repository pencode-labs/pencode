package remote

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/internal/version"
	"github.com/pencode-labs/pencode/output"
)

func TestSSHReleaseDownloadInstallAndReuse(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX SSH host fixture")
	}
	const release = "v1.2.3"
	binary := filepath.Join(t.TempDir(), "server")
	build := exec.CommandContext(t.Context(), "go", "build", "-ldflags=-X "+version.Symbol+"="+release, "-o", binary, "./cmd/pencode-server")
	build.Dir = ".."
	if data, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v: %s", err, data)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	platform := Platform{runtime.GOOS, runtime.GOARCH}
	name := "pencode-server-" + release + "-" + platform.String()
	if got := (Connector{Version: release}).releaseAddress(platform); got != "https://github.com/pencode-labs/pencode/releases/download/"+release+"/"+name {
		t.Fatal("unexpected release URL", got)
	}
	for _, fixture := range []struct {
		name    string
		initial string
		failure string
	}{
		{name: "missing"},
		{name: "upgrade", initial: "v0.9.0"},
		{name: "downgrade", initial: "v9.9.9"},
		{name: "unversioned", initial: "unknown"},
		{name: "corrupt download", initial: "v0.9.0", failure: "SHA-256 校验失败"},
		{name: "missing release", initial: "v0.9.0", failure: "HTTP 404"},
		{name: "wrong checksum entry", initial: "v0.9.0", failure: "有效 SHA-256"},
		{name: "wrong server version", initial: "v0.9.0", failure: "版本不匹配"},
		{name: "interrupted download", initial: "v0.9.0", failure: "unexpected EOF"},
		{name: "canceled download", initial: "v0.9.0", failure: "context canceled"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			required := release
			if fixture.name == "wrong server version" {
				required = "v1.2.4"
			}
			assetName := "pencode-server-" + required + "-" + platform.String()
			root := filepath.Join(t.TempDir(), "remote's servers")
			installed := filepath.Join(root, "pencode-server")
			var original []byte
			if fixture.initial != "" {
				if err := os.MkdirAll(root, 0o700); err != nil {
					t.Fatal(err)
				}
				original = []byte("#!/bin/sh\nif [ \"${1:-}\" = --version ]; then printf '%s\\n' " + shellQuote(fixture.initial) + "; exit; fi\nexit 99\n")
				if err := os.WriteFile(installed, original, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			tools := t.TempDir()
			writeScript := func(name, script string) string {
				t.Helper()
				path := filepath.Join(tools, name)
				if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o700); err != nil {
					t.Fatal(err)
				}
				return path
			}
			// An outdated PATH binary must not cause another download after install.
			writeScript("pencode-server", "printf 'v0.0.1\\n'\n")
			t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("SHELL", writeScript("login-shell", "exec /bin/sh -c \"$2\"\n"))
			ssh := writeScript("ssh", "for command do :; done\nexec /bin/sh -c \"$command\"\n")
			cache := t.TempDir()
			t.Setenv("TMPDIR", cache)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if fixture.name == "missing release" {
					http.NotFound(w, r)
					return
				}
				if r.URL.Path == "/"+required+"/SHA256SUMS" {
					digest := sha256.Sum256(data)
					asset := assetName
					if fixture.name == "wrong checksum entry" {
						asset += ".other"
					}
					fmt.Fprintf(w, "%x  other-asset\n%x  %s\n", digest, digest, asset)
					return
				}
				if r.URL.Path != "/"+required+"/"+assetName {
					t.Errorf("unexpected download path: %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				if fixture.name == "canceled download" {
					cancel()
					return
				}
				if fixture.name == "interrupted download" {
					w.Header().Set("Content-Length", fmt.Sprint(len(data)+100))
				}
				w.Write(data)
				if fixture.name == "corrupt download" {
					w.Write([]byte("corruption"))
				}
			}))
			defer server.Close()
			var store output.Store
			logs := store.Channel(OutputChannelName)
			connector := Connector{Executable: ssh, Version: required, ReleaseURL: server.URL, HTTP: server.Client(), Output: logs}
			target := Target{Host: "fixture", ServerRoot: root}
			stream, err := connector.Connect(ctx, target, nil)
			if fixture.failure != "" {
				if err == nil {
					stream.Close()
					t.Fatal("accepted invalid server installation")
				}
				if !strings.Contains(err.Error(), fixture.failure) {
					t.Fatalf("error = %v, want %s", err, fixture.failure)
				}
				got, readErr := os.ReadFile(installed)
				if readErr != nil || !bytes.Equal(got, original) {
					t.Fatal("failed install changed the previous server", readErr)
				}
				if entries, err := os.ReadDir(root); err != nil || len(entries) != 1 {
					t.Fatal("failed install retained remote staging files", entries, err)
				}
				if text, _ := logs.Snapshot(); !strings.Contains(text, "连接失败") {
					t.Fatal("failure was not logged to Output", text)
				}
			} else {
				if err != nil {
					text, _ := logs.Snapshot()
					t.Fatalf("connect: %v\n%s", err, text)
				}
				backend, err := client.New(ctx, stream)
				if err != nil {
					stream.Close()
					t.Fatal("bootstrap logs corrupted the protocol", err)
				}
				backend.Close()
				got, err := os.ReadFile(installed)
				if err != nil || !bytes.Equal(got, data) {
					t.Fatal("matching server was not installed", err)
				}
				stream, err = connector.Connect(ctx, target, nil)
				if err != nil {
					t.Fatal("reconnect", err)
				}
				stream.Close()
				if requests.Load() != 2 {
					t.Fatal("reconnect downloaded the server again", requests.Load())
				}
				text, _ := logs.Snapshot()
				for _, message := range []string{"GitHub", "SHA-256 校验通过", "上传并安装", release, "已启动", "使用"} {
					if !strings.Contains(text, message) {
						t.Errorf("Output missing %q: %s", message, text)
					}
				}
			}
			if entries, err := os.ReadDir(cache); err != nil || len(entries) != 0 {
				t.Fatal("download retained temporary files", entries, err)
			}
		})
	}
}
