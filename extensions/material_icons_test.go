package extensions

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/pencode-labs/pencode/internal/extensionbuild"
	"github.com/pencode-labs/pencode/peb"
)

// Build and install the Go extension, then exercise the real host handshake and
// resource cache without starting the editor.
func TestMaterialIconsRegistersIconTheme(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "material-icons")
	if err := os.MkdirAll(filepath.Join(directory, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(directory, "resources"), os.DirFS("material-icons/resources")); err != nil {
		t.Fatal(err)
	}
	entry := "bin/material-icons"
	if runtime.GOOS == "windows" {
		entry += ".exe"
	}
	manifestPath := filepath.Join(directory, "resources", "extension.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Entry = entry
	data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "build", "-p", "1", "-o", filepath.Join(directory, filepath.FromSlash(entry)), ".")
	command.Dir = "material-icons"
	command.Env, err = extensionbuild.Environment(t.TempDir(), "..", command.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build material-icons: %v\n%s", err, output)
	}
	// The installed executable must carry its rule table with it.
	if err := os.Remove(filepath.Join(directory, "resources", "rules.psv")); err != nil {
		t.Fatal(err)
	}
	bundle, err := Load(context.Background(), extensionBackend(t, filepath.Dir(directory)), "material-icons")
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Themes) != 0 || len(bundle.IconThemes) != 1 {
		t.Fatalf("contributions: themes=%d iconThemes=%d", len(bundle.Themes), len(bundle.IconThemes))
	}
	theme := bundle.IconThemes[0]
	if theme.ID != "extensions/material-icons/material-design-icons" || theme.Label != "Material Design Icons" {
		t.Fatalf("icon theme: %s %s", theme.ID, theme.Label)
	}
	if len(theme.Rules) < 7000 {
		t.Fatalf("rule table is too small: %d", len(theme.Rules))
	}
	// Every rule resolves to a cached SVG, and no cached SVG is left unreachable,
	// which would mean the table dropped an icon it still references.
	sources := make(map[string]bool)
	type ruleKey struct {
		match   peb.Match
		pattern string
	}
	rules := make(map[ruleKey]string, len(theme.Rules))
	for _, rule := range theme.Rules {
		sources[rule.Path] = true
		if len(bundle.Icons[rule.Path]) == 0 {
			t.Fatalf("rule %q has no cached icon at %s", rule.Pattern, rule.Path)
		}
		key := ruleKey{rule.Match, rule.Pattern}
		if previous, exists := rules[key]; exists && previous != rule.Path {
			t.Fatalf("conflicting rules for %q: %s and %s", rule.Pattern, previous, rule.Path)
		}
		rules[key] = rule.Path
	}
	if len(sources) != len(bundle.Icons) {
		t.Fatalf("rules use %d icons but %d are cached", len(sources), len(bundle.Icons))
	}
	// Spot checks against the upstream manifest after parsing and registration.
	for _, expected := range []struct {
		match   peb.Match
		pattern string
		icon    string
	}{
		{peb.FileSuffix, ".go", "go"},
		{peb.FileSuffix, ".ts", "typescript"},
		{peb.FileSuffix, ".json", "json"},
		{peb.FileName, "go.mod", "go-mod"},
		{peb.FileName, "dockerfile", "docker"},
		{peb.FolderName, "src", "folder-src"},
		{peb.FolderName, ".github", "folder-github"},
		{peb.DefaultFile, "", "file"},
		{peb.DefaultFolder, "", "folder"},
	} {
		if got := rules[ruleKey{expected.match, expected.pattern}]; got != "peb://material-icons/resources/icons/"+expected.icon+".svg" {
			t.Fatalf("rule %d %q = %q, want %q", expected.match, expected.pattern, got, expected.icon)
		}
	}
}
