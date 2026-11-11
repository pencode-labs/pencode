package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
)

func TestWindowsFileAssociations(t *testing.T) {
	compiler := os.Getenv("ISCC")
	if compiler == "" {
		compiler = "iscc"
	}
	if _, err := exec.LookPath(compiler); err != nil {
		t.Skipf("native installer compiler unavailable: %v", err)
	}
	root := t.TempDir()
	script, err := os.ReadFile(filepath.Join("..", "..", "packaging", "windows.iss"))
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the real installer with an isolated registry namespace, app ID
	// and no shortcuts, so the test cannot replace an installed editor's data.
	name := fmt.Sprintf("PencodeAssociationTest-%d-%s", os.Getpid(), filepath.Base(root))
	key := `Software\PencodeInstallerTests\` + name
	text := strings.ReplaceAll(string(script), `Software\Classes`, key)
	text = strings.ReplaceAll(text, "AppId={{438863BF-885C-4AC6-A981-9C8F43563985}", "AppId="+name)
	text = strings.ReplaceAll(text, "AppName=Pencode", "AppName="+name)
	text = strings.ReplaceAll(text, "CloseApplications=yes", "CloseApplications=no")
	before, after, ok := strings.Cut(text, "[Icons]")
	if !ok {
		t.Fatal("installer has no icons section")
	}
	_, after, ok = strings.Cut(after, "[Registry]")
	if !ok {
		t.Fatal("installer has no registry section")
	}
	text = before + "[Registry]" + after
	project := filepath.Join(root, "project")
	writeTestFile(t, filepath.Join(project, "packaging", "windows.iss"), text, 0o600)
	stage := filepath.Join(root, "stage")
	writeTestFile(t, filepath.Join(stage, "pencode", "pencode.exe"), "fixture", 0o600)
	installer := filepath.Join(root, "pencode-test.exe")
	if err := buildInstaller(stage, installer, project, "1.2.3"); err != nil {
		t.Fatal(err)
	}
	defaults, _, err := registry.CreateKey(registry.CURRENT_USER, key+`\.txt`, registry.ALL_ACCESS)
	if err != nil {
		t.Fatal(err)
	}
	if err := defaults.SetStringValue("", "Existing.App"); err != nil {
		t.Fatal(err)
	}
	defaults.Close()
	t.Cleanup(func() {
		if output, err := exec.Command("reg.exe", "delete", `HKCU\`+key, "/f").CombinedOutput(); err != nil {
			t.Errorf("remove isolated test registry: %v: %s", err, output)
		}
	})
	installed := filepath.Join(root, "installed editor 中文")
	run := func(executable string, args ...string) {
		t.Helper()
		log := filepath.Join(root, "installer.log")
		args = append(args, "/LOG="+log)
		if err := exec.Command(executable, args...).Run(); err != nil {
			diagnostics, _ := os.ReadFile(log)
			t.Fatalf("installer command: %v\n%s", err, diagnostics)
		}
	}
	run(installer, "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/NOICONS", "/TASKS=", "/DIR="+installed)
	uninstall := filepath.Join(installed, "unins000.exe")
	uninstalled := false
	t.Cleanup(func() {
		if !uninstalled {
			run(uninstall, "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART")
		}
	})
	read := func(subkey, value string) string {
		t.Helper()
		k, err := registry.OpenKey(registry.CURRENT_USER, key+`\`+subkey, registry.QUERY_VALUE)
		if err != nil {
			t.Fatal(err)
		}
		defer k.Close()
		got, _, err := k.GetStringValue(value)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	wantCommand := `"` + filepath.Join(installed, "pencode.exe") + `" -- "%1"`
	for _, subkey := range []string{`Applications\pencode.exe\shell\open\command`, `Pencode.File\shell\open\command`} {
		if got := read(subkey, ""); got != wantCommand {
			t.Errorf("open command: %q, want %q", got, wantCommand)
		}
	}
	for _, extension := range []string{".txt", ".go", ".json", ".md", ".py"} {
		read(`Applications\pencode.exe\SupportedTypes`, extension)
		read(extension+`\OpenWithProgids`, "Pencode.File")
	}
	if got := read(".txt", ""); got != "Existing.App" {
		t.Fatalf("installer replaced existing default: %q", got)
	}
	run(uninstall, "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART")
	uninstalled = true
	if got := read(".txt", ""); got != "Existing.App" {
		t.Fatalf("uninstaller removed existing default: %q", got)
	}
	for _, subkey := range []string{`Applications\pencode.exe`, `Pencode.File`, `.txt\OpenWithProgids`, `.go\OpenWithProgids`} {
		k, err := registry.OpenKey(registry.CURRENT_USER, key+`\`+subkey, registry.QUERY_VALUE)
		if err == nil {
			k.Close()
			t.Errorf("uninstaller left association %s", subkey)
		} else if err != registry.ErrNotExist {
			t.Fatal(err)
		}
	}
}
