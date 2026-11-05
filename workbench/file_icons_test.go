package workbench

import (
	"os"
	"strings"
	"testing"

	"github.com/pencode-labs/pencode/peb"
)

func TestFileIconsMatchNamesWithoutCase(t *testing.T) {
	icons := newPencodeFileIcons(peb.IconTheme{Rules: []peb.IconRule{
		{Match: peb.DefaultFile, Path: "file"},
		{Match: peb.DefaultFolder, Path: "folder"},
		{Match: peb.FileName, Pattern: "Dockerfile", Path: "docker"},
		{Match: peb.FileName, Pattern: "Dockerfile.BETA", Path: "docker-beta"},
		{Match: peb.FileName, Pattern: "LICENSE", Path: "old-license"},
		{Match: peb.FileName, Pattern: "license", Path: "license"},
		{Match: peb.FileName, Pattern: "TYPES.D.TS", Path: "types"},
		{Match: peb.FileName, Pattern: "ÉCOLE", Path: "unicode"},
		{Match: peb.FileSuffix, Pattern: ".TS", Path: "typescript"},
		{Match: peb.FileSuffix, Pattern: ".D.TS", Path: "declaration"},
		{Match: peb.FolderName, Pattern: "Src", Path: "source"},
	}})
	for _, test := range []struct {
		name      string
		want      string
		directory bool
	}{
		{name: "Dockerfile", directory: false, want: "docker"},
		{name: "DOCKERFILE", directory: false, want: "docker"},
		{name: "dockerfile.beta", directory: false, want: "docker-beta"},
		{name: "Dockerfile.Beta", directory: false, want: "docker-beta"},
		{name: "LICENSE", directory: false, want: "license"},
		{name: "École", directory: false, want: "unicode"},
		{name: "TYPES.d.ts", directory: false, want: "types"},
		{name: "component.D.TS", directory: false, want: "declaration"},
		{name: "component.Ts", directory: false, want: "typescript"},
		{name: "SRC", directory: true, want: "source"},
		{name: "SRC", directory: false, want: "file"},
		{name: "Dockerfile", directory: true, want: "folder"},
		{name: "unknown", directory: false, want: "file"},
		{name: "unknown", directory: true, want: "folder"},
		{name: strings.Repeat("A", 300) + ".TS", directory: false, want: "typescript"},
	} {
		if got := icons.source(test.name, test.directory); got != test.want {
			t.Errorf("source(%q, %t) = %q, want %q", test.name, test.directory, got, test.want)
		}
		if len(test.name) <= 256 {
			if n := testing.AllocsPerRun(100, func() { icons.source(test.name, test.directory) }); n != 0 {
				t.Errorf("source(%q) allocated %g times", test.name, n)
			}
		}
	}
	if (*pencodeFileIcons)(nil).source("Dockerfile", false) != "" || newPencodeFileIcons(peb.IconTheme{}).source("Dockerfile", false) != "" {
		t.Fatal("empty themes must fall back to built-in icons")
	}
}

func TestMaterialIconsResolveBundledFileNames(t *testing.T) {
	data, err := os.ReadFile("../extensions/material-icons/resources/rules.psv")
	if err != nil {
		t.Fatal(err)
	}
	var theme peb.IconTheme
	for line := range strings.Lines(string(data)) {
		pattern, icon, ok := strings.Cut(strings.TrimSuffix(line[2:], "\n"), "\x1f")
		if !ok {
			t.Fatalf("invalid bundled rule %q", line)
		}
		theme.Rules = append(theme.Rules, peb.IconRule{Match: peb.Match(line[0] - '0'), Pattern: pattern, Path: icon})
	}
	icons := newPencodeFileIcons(theme)
	for name, want := range map[string]string{
		"Dockerfile": "docker", "Dockerfile.beta": "docker", "Containerfile": "docker",
		"Makefile": "makefile", "LICENSE": "license", "README.md": "readme",
		".dockerignore": "docker", ".gitignore": "git", "main.GO": "go",
	} {
		if got := icons.source(name, false); got != want {
			t.Errorf("source(%q) = %q, want %q", name, got, want)
		}
	}
}
