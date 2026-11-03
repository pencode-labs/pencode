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
		directory bool
		want      string
	}{
		{"Dockerfile", false, "docker"},
		{"DOCKERFILE", false, "docker"},
		{"dockerfile.beta", false, "docker-beta"},
		{"Dockerfile.Beta", false, "docker-beta"},
		{"LICENSE", false, "license"},
		{"École", false, "unicode"},
		{"TYPES.d.ts", false, "types"},
		{"component.D.TS", false, "declaration"},
		{"component.Ts", false, "typescript"},
		{"SRC", true, "source"},
		{"SRC", false, "file"},
		{"Dockerfile", true, "folder"},
		{"unknown", false, "file"},
		{"unknown", true, "folder"},
		{strings.Repeat("A", 300) + ".TS", false, "typescript"},
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
