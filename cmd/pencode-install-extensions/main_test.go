package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/pencode-labs/pencode/server/extensionhost"
)

func TestInstallReplacesExtension(t *testing.T) {
	source := filepath.Join(t.TempDir(), "extension source")
	if err := os.MkdirAll(filepath.Join(source, "resources"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod":                   "module example.test/extension\n\ngo 1.27.0\n",
		"main.go":                  "package main\nfunc main() { println(\"extension ready\") }\n",
		"LICENSE":                  "fixture license\n",
		"resources/extension.json": `{"id":"test","entry":"bin/test","name":"Test","args":["--test"]}`,
		"resources/theme.css":      "body {}\n",
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(source, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Each extension must ignore a workspace and use its own module unchanged.
	t.Setenv("GOWORK", filepath.Join(t.TempDir(), "missing.work"))
	directory := t.TempDir()
	target := filepath.Join(directory, "test")
	for range 2 {
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, "obsolete"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := install("go", source, directory); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(target, "obsolete")); !os.IsNotExist(err) {
			t.Fatalf("old file remains: %v", err)
		}
	}
	data, err := os.ReadFile(filepath.Join(target, "resources", "extension.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest extensionhost.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Entry != executable("bin/test", runtime.GOOS) || manifest.Name != "Test" || len(manifest.Args) != 1 || manifest.Args[0] != "--test" {
		t.Fatalf("manifest changed: %+v", manifest)
	}
	binary := filepath.Join(target, filepath.FromSlash(manifest.Entry))
	if output, err := exec.Command(binary).CombinedOutput(); err != nil || string(output) != "extension ready\n" {
		t.Fatalf("installed executable: %v, %q", err, output)
	}
	for _, name := range []string{"LICENSE", "resources/theme.css"} {
		data, err := os.ReadFile(filepath.Join(target, name))
		if err != nil || string(data) != files[name] {
			t.Fatalf("installed %s: %v, %q", name, err, data)
		}
	}
	data, err = os.ReadFile(filepath.Join(source, "go.mod"))
	if err != nil || string(data) != files["go.mod"] {
		t.Fatalf("source go.mod changed: %v, %q", err, data)
	}
	data, err = os.ReadFile(filepath.Join(source, "resources", "extension.json"))
	if err != nil || string(data) != files["resources/extension.json"] {
		t.Fatalf("source manifest changed: %v, %q", err, data)
	}
	if err := os.WriteFile(filepath.Join(source, "main.go"), []byte("invalid Go source"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := install("go", source, directory); err == nil {
		t.Fatal("installed a failed build")
	}
	if _, err := os.Stat(binary); err != nil {
		t.Fatalf("failed build removed the existing extension: %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || entries[0].Name() != "test" {
		t.Fatalf("staging directories remain: %v, %v", entries, err)
	}
	files["resources/extension.json"] = `{"id":"..","entry":"bin/test"}`
	if err := os.WriteFile(filepath.Join(source, "resources", "extension.json"), []byte(files["resources/extension.json"]), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := install("go", source, directory); err == nil {
		t.Fatal("accepted an extension ID outside its installation directory")
	}
}

func TestExecutablePlatforms(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		want := "bin/test"
		if goos == "windows" {
			want += ".exe"
		}
		for _, entry := range []string{"bin/test", "bin/test.exe"} {
			if got := executable(entry, goos); got != want {
				t.Errorf("executable(%q, %q) = %q, want %q", entry, goos, got, want)
			}
		}
	}
}
