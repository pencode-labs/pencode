// pencode-install-extensions builds independent extension modules for this host.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pencode-labs/pencode/server/extensionhost"
)

func main() {
	goCommand := flag.String("go", "go", "Go executable")
	directory := flag.String("directory", "", "Installation directory (default: ~/.pencode/extensions)")
	flag.Parse()
	if err := installAll(*goCommand, *directory); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func installAll(goCommand, directory string) error {
	if directory == "" {
		var err error
		directory, err = extensionhost.Directory()
		if err != nil {
			return err
		}
	}
	entries, err := os.ReadDir("extensions")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		source := filepath.Join("extensions", entry.Name())
		if err := install(goCommand, source, directory); err != nil {
			return fmt.Errorf("install %s: %w", source, err)
		}
	}
	return nil
}

func install(goCommand, source, directory string) error {
	if _, err := os.Stat(filepath.Join(source, "go.mod")); err != nil {
		return fmt.Errorf("independent go.mod required: %w", err)
	}
	data, err := os.ReadFile(filepath.Join(source, "resources", "extension.json"))
	if err != nil {
		return err
	}
	var manifest extensionhost.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return err
	}
	if !extensionhost.ValidID(manifest.ID) || !extensionhost.ValidResourcePath(manifest.Entry, false) {
		return fmt.Errorf("invalid extension ID or entry")
	}
	manifest.Entry = executable(manifest.Entry, runtime.GOOS)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(directory, "_install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	stage, err = filepath.Abs(stage)
	if err != nil {
		return err
	}
	if err := os.CopyFS(filepath.Join(stage, "resources"), os.DirFS(filepath.Join(source, "resources"))); err != nil {
		return err
	}
	license, err := os.ReadFile(filepath.Join(source, "LICENSE"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, "LICENSE"), license, 0o644); err != nil {
		return err
	}
	command := exec.Command(goCommand, "build", "-mod=readonly", "-o", filepath.Join(stage, filepath.FromSlash(manifest.Entry)), ".")
	command.Dir = source
	command.Env = append(os.Environ(), "GOWORK=off")
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		return err
	}
	data, err = json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, "resources", "extension.json"), append(data, '\n'), 0o644); err != nil {
		return err
	}
	target := filepath.Join(directory, manifest.ID)
	if err := os.RemoveAll(target); err != nil {
		return err
	}
	if err := os.Rename(stage, target); err != nil {
		return err
	}
	fmt.Println("Installed", manifest.ID, "in", target)
	return nil
}

func executable(entry, goos string) string {
	entry = strings.TrimSuffix(entry, ".exe")
	if goos == "windows" {
		entry += ".exe"
	}
	return entry
}
