package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// CI runs this with the native packaging tools on each supported architecture.
func TestNativeInstaller(t *testing.T) {
	compiler := map[string]string{"windows": os.Getenv("ISCC"), "linux": "dpkg-deb", "darwin": "productbuild"}[runtime.GOOS]
	if compiler == "" {
		compiler = "iscc"
	}
	if _, err := exec.LookPath(compiler); err != nil {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatal(err)
		}
		t.Skipf("native installer compiler unavailable: %v", err)
	}
	project, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	stage := t.TempDir()
	payload := filepath.Join(stage, "Pencode")
	binary := filepath.Join(payload, "Pencode")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if runtime.GOOS == "darwin" {
		bundle := filepath.Join(payload, "Pencode.app", "Contents")
		binary = filepath.Join(bundle, "MacOS", "Pencode")
		writeTestFile(t, filepath.Join(bundle, "Info.plist"), `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>CFBundleExecutable</key><string>Pencode</string><key>CFBundleIdentifier</key><string>dev.pencode.desktop</string><key>CFBundlePackageType</key><string>APPL</string><key>CFBundleVersion</key><string>1.2.3</string></dict></plist>`, 0o644)
	}
	writeTestFile(t, binary, "#!/bin/sh\nexit 0\n", 0o755)
	if runtime.GOOS == "darwin" {
		command := exec.Command("clang", "-x", "c", "-", "-o", binary)
		command.Stdin = strings.NewReader("int main(void) { return 0; }")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("compile macOS fixture: %v: %s", err, output)
		}
	}
	for _, name := range []string{"pencode-version", "LICENSE", "README.md", "README.zh-CN.md", "licenses/ONIX-LICENSE.txt", "resources/catalog.bin", "resources/base.dat"} {
		writeTestFile(t, filepath.Join(payload, name), "fixture", 0o644)
	}
	destination := filepath.Join(t.TempDir(), "pencode-test"+installerExtension(runtime.GOOS))
	if err := buildInstaller(stage, destination, project, "v1.2.3-rc.1"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(destination); err != nil || info.Size() == 0 {
		t.Fatalf("installer missing or empty: %v", err)
	}
	if runtime.GOOS == "darwin" {
		bundle := filepath.Join(payload, "Pencode.app")
		for _, name := range []string{"pencode-version", "LICENSE", "README.md", "README.zh-CN.md", "licenses/ONIX-LICENSE.txt"} {
			if _, err := os.Stat(filepath.Join(bundle, "Contents", "Resources", name)); err != nil {
				t.Fatalf("missing bundle resource %s: %v", name, err)
			}
		}
		if output, err := exec.Command("codesign", "--verify", "--strict", bundle).CombinedOutput(); err != nil {
			t.Fatalf("verify macOS signature: %v: %s", err, output)
		}
		plist := filepath.Join(bundle, "Contents", "Info.plist")
		output, err := exec.Command("plutil", "-extract", "CFBundleDocumentTypes", "xml1", "-o", "-", plist).CombinedOutput()
		if err != nil || !strings.Contains(string(output), "public.data") || !strings.Contains(string(output), "public.folder") || !strings.Contains(string(output), "Alternate") {
			t.Fatalf("missing macOS document handlers: %v: %s", err, output)
		}
	}
	if runtime.GOOS == "linux" {
		listing, err := exec.Command("dpkg-deb", "--contents", destination).CombinedOutput()
		if err != nil {
			t.Fatalf("inspect deb: %v: %s", err, listing)
		}
		for _, name := range []string{"./opt/pencode/Pencode", "./opt/pencode/resources/catalog.bin", "./opt/pencode/pencode-version", "./usr/bin/pencode", "./usr/share/applications/pencode.desktop"} {
			if !strings.Contains(string(listing), name) {
				t.Errorf("installer missing %s", name)
			}
		}
		extracted := t.TempDir()
		if output, err := exec.Command("dpkg-deb", "--raw-extract", destination, extracted).CombinedOutput(); err != nil {
			t.Fatalf("extract deb: %v: %s", err, output)
		}
		desktop := filepath.Join(extracted, "usr", "share", "applications", "pencode.desktop")
		data, err := os.ReadFile(desktop)
		if err != nil || !strings.Contains(string(data), " -- %F") || !strings.Contains(string(data), "MimeType=text/plain;") || strings.Contains(string(data), "x-scheme-handler") {
			t.Fatalf("invalid local file handler: %v: %s", err, data)
		}
		if validator, err := exec.LookPath("desktop-file-validate"); err == nil {
			if output, err := exec.Command(validator, desktop).CombinedOutput(); err != nil {
				t.Fatalf("validate desktop file: %v: %s", err, output)
			}
		}
		if updater, err := exec.LookPath("update-desktop-database"); err == nil {
			directory := filepath.Dir(desktop)
			if output, err := exec.Command(updater, "-q", directory).CombinedOutput(); err != nil {
				t.Fatalf("build isolated MIME cache: %v: %s", err, output)
			}
			cache, err := os.ReadFile(filepath.Join(directory, "mimeinfo.cache"))
			if err != nil || !strings.Contains(string(cache), "text/plain=pencode.desktop;") || !strings.Contains(string(cache), "inode/directory=pencode.desktop;") {
				t.Fatalf("Pencode is missing from MIME associations: %v: %s", err, cache)
			}
		}
		for _, name := range []string{"postinst", "postrm"} {
			name = filepath.Join(extracted, "DEBIAN", name)
			info, err := os.Stat(name)
			if err != nil || info.Mode()&0o111 == 0 {
				t.Fatalf("missing executable desktop-cache hook %s: %v", name, err)
			}
			if output, err := exec.Command("sh", "-n", name).CombinedOutput(); err != nil {
				t.Fatalf("invalid desktop-cache hook: %v: %s", err, output)
			}
		}
	}
}

func TestNativePackageVersions(t *testing.T) {
	for _, version := range []string{"v1.2.3", "v1.2.3-rc.1", "dev-abc123"} {
		want := map[string]string{"v1.2.3": "1.2.3", "v1.2.3-rc.1": "1.2.3~rc.1", "dev-abc123": "0.0.0~dev-abc123"}[version]
		if got := nativePackageVersion(version, "linux"); got != want {
			t.Errorf("Debian version %s: %s, want %s", version, got, want)
		}
		want = "1.2.3"
		if strings.HasPrefix(version, "dev") {
			want = "0.0.0"
		}
		if got := nativePackageVersion(version, "darwin"); got != want {
			t.Errorf("macOS version %s: %s, want %s", version, got, want)
		}
	}
}
