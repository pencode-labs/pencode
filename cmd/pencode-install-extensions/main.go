// pencode-install-extensions builds independent extension modules for this host.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pencode-labs/pencode/internal/extensionbuild"
	"github.com/pencode-labs/pencode/internal/systemproxy"
	"github.com/pencode-labs/pencode/server/extensionhost"
)

func main() {
	systemproxy.Configure()
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
		if err := install(goCommand, source, directory, "."); err != nil {
			return fmt.Errorf("install %s: %w", source, err)
		}
	}
	return nil
}

func install(goCommand, source, directory, sdk string) error {
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
	if !manifest.JavaScript() {
		manifest.Entry = executable(manifest.Entry, runtime.GOOS)
	}
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
	if err := build(goCommand, source, stage, sdk, manifest); err != nil {
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
	if err := replaceExtension(stage, target); err != nil {
		return err
	}
	fmt.Println("Installed", manifest.ID, "in", target)
	return nil
}

func replaceExtension(stage, target string) error {
	backup, err := os.MkdirTemp(filepath.Dir(target), "_install-previous-")
	if err != nil {
		return err
	}
	previous := filepath.Join(backup, "extension")
	if err := os.Rename(target, previous); err != nil && !os.IsNotExist(err) {
		_ = os.Remove(backup)
		return fmt.Errorf("move existing extension aside (close pencode if it is in use): %w", err)
	}
	if err := os.Rename(stage, target); err != nil {
		if _, statErr := os.Stat(previous); statErr == nil {
			if rollbackErr := os.Rename(previous, target); rollbackErr != nil {
				return errors.Join(err, fmt.Errorf("restore extension from %s: %w", previous, rollbackErr))
			}
		}
		_ = os.Remove(backup)
		return err
	}
	// Windows may retain the running executable until its process exits.
	if err := os.RemoveAll(backup); err != nil {
		fmt.Fprintln(os.Stderr, "Installed extension; previous files remain in", backup, "(remove after closing pencode):", err)
	}
	return nil
}

func build(goCommand, source, stage, sdk string, manifest extensionhost.Manifest) error {
	if manifest.JavaScript() {
		// JavaScript packages bundle their dependencies beside the entry. Only
		// this output directory is installed, never node_modules or TS sources.
		output := filepath.Dir(filepath.FromSlash(manifest.Entry))
		if output == "." || output == "resources" || strings.HasPrefix(filepath.ToSlash(output), "resources/") {
			return fmt.Errorf("JavaScript extension entry must use a separate output directory, e.g. dist/main.mjs")
		}
		manager, err := exec.LookPath("npm")
		installArgs := []string{"ci", "--no-audit", "--no-fund"}
		buildArgs := []string{"run", "build"}
		if err != nil {
			manager, err = exec.LookPath("bun")
			installArgs = []string{"install", "--frozen-lockfile"}
			buildArgs = []string{"--bun", "run", "build"}
		}
		if err != nil {
			return fmt.Errorf("building JavaScript extensions requires npm or Bun on the build host")
		}
		for _, args := range [][]string{installArgs, buildArgs} {
			command := exec.Command(manager, args...)
			command.Dir = source
			command.Stdout, command.Stderr = os.Stdout, os.Stderr
			if err := command.Run(); err != nil {
				return err
			}
		}
		entry, err := os.Stat(filepath.Join(source, filepath.FromSlash(manifest.Entry)))
		if err != nil {
			return err
		}
		if !entry.Mode().IsRegular() {
			return fmt.Errorf("JavaScript extension entry is not a regular file")
		}
		// Preserve type:module for .js entries. Runtime dependencies still need
		// to be bundled; installing a package never runs a package manager.
		data, err := os.ReadFile(filepath.Join(source, "package.json"))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(stage, "package.json"), data, 0o644); err != nil {
			return err
		}
		return os.CopyFS(filepath.Join(stage, output), os.DirFS(filepath.Join(source, output)))
	}
	if _, err := os.Stat(filepath.Join(source, "go.mod")); err != nil {
		return fmt.Errorf("independent go.mod required: %w", err)
	}
	buildDirectory, err := os.MkdirTemp("", "pencode-extension-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(buildDirectory)
	command := exec.Command(goCommand, "build", "-mod=readonly", "-o", filepath.Join(stage, filepath.FromSlash(manifest.Entry)), ".")
	command.Dir = source
	command.Env, err = extensionbuild.Environment(buildDirectory, sdk, source)
	if err != nil {
		return err
	}
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	return command.Run()
}

func executable(entry, goos string) string {
	entry = strings.TrimSuffix(entry, ".exe")
	if goos == "windows" {
		entry += ".exe"
	}
	return entry
}
