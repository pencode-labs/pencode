package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func installerExtension(platform string) string {
	switch platform {
	case "windows":
		return ".exe"
	case "darwin":
		return ".pkg"
	default:
		return ".deb"
	}
}

func runPackaging(name string, args ...string) error {
	command := exec.Command(name, args...)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("package installer with %s: %w", name, err)
	}
	return nil
}

// Keep prereleases older than stable versions in apt's version ordering.
// The original tag remains in pencode-version and in the output filename.
func nativePackageVersion(version, platform string) string {
	version = strings.ReplaceAll(strings.TrimPrefix(version, "v"), "_", ".")
	if version == "" || version[0] < '0' || version[0] > '9' {
		version = "0.0.0-" + version
	}
	if platform == "linux" {
		return strings.Replace(version, "-", "~", 1)
	}
	version, _, _ = strings.Cut(version, "-")
	version, _, _ = strings.Cut(version, "+")
	return version
}

func buildInstaller(stage, destination, project, version string) error {
	payload := filepath.Join(stage, "Pencode")
	packageVersion := nativePackageVersion(version, runtime.GOOS)
	switch runtime.GOOS {
	case "windows":
		associations := filepath.Join(stage, "file-associations.iss")
		if err := os.WriteFile(associations, []byte(windowsFileAssociations()), 0o644); err != nil {
			return err
		}
		compiler := os.Getenv("ISCC")
		if compiler == "" {
			compiler = "iscc"
		}
		architecture := "x64compatible"
		if runtime.GOARCH == "arm64" {
			architecture = "arm64"
		}
		return runPackaging(compiler, "/DSourceDir="+payload, "/DOutputDir="+filepath.Dir(destination),
			"/DOutputName="+strings.TrimSuffix(filepath.Base(destination), ".exe"),
			"/DAppVersion="+version, "/DAppArchitecture="+architecture,
			"/DAssociationsFile="+associations,
			filepath.Join(project, "packaging", "windows.iss"))
	case "darwin":
		bundle := filepath.Join(payload, "Pencode.app")
		// Contents is reserved for signed code; data belongs in Resources.
		resources := filepath.Join(bundle, "Contents", "Resources")
		if err := os.MkdirAll(resources, 0o755); err != nil {
			return err
		}
		for _, name := range []string{"pencode-version", "LICENSE", "README.md", "README.zh-CN.md", "licenses"} {
			if err := os.Rename(filepath.Join(payload, name), filepath.Join(resources, name)); err != nil {
				return err
			}
		}
		if err := configureMacOSBundle(bundle); err != nil {
			return err
		}
		return runPackaging("productbuild", "--identifier", "dev.pencode.desktop", "--version", packageVersion,
			"--component", bundle, "/Applications", destination)
	case "linux":
		root := filepath.Join(stage, "deb")
		for _, dir := range []string{"DEBIAN", "opt", "usr/bin", "usr/share/applications", "usr/share/icons/hicolor/scalable/apps"} {
			if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
				return err
			}
		}
		if err := os.Rename(payload, filepath.Join(root, "opt", "pencode")); err != nil {
			return err
		}
		control := fmt.Sprintf("Package: pencode\nVersion: %s\nSection: devel\nPriority: optional\nArchitecture: %s\nMaintainer: Pencode contributors\nHomepage: https://github.com/pencode-labs/pencode\nDepends: libc6, libx11-6, libxext6, libxrender1, libxi6, libwayland-client0, libwayland-cursor0, libwayland-egl1, libxkbcommon0, libgl1, desktop-file-utils\nDescription: Pencode code editor\n", packageVersion, runtime.GOARCH)
		if err := os.WriteFile(filepath.Join(root, "DEBIAN", "control"), []byte(control), 0o644); err != nil {
			return err
		}
		if err := os.Symlink("/opt/pencode/Pencode", filepath.Join(root, "usr", "bin", "pencode")); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(root, "usr", "share", "applications", "pencode.desktop"), []byte(linuxDesktopEntry), 0o644); err != nil {
			return err
		}
		for _, name := range []string{"postinst", "postrm"} {
			if err := os.WriteFile(filepath.Join(root, "DEBIAN", name), []byte("#!/bin/sh\nset -e\nif command -v update-desktop-database >/dev/null 2>&1; then\n    update-desktop-database -q /usr/share/applications\nfi\n"), 0o755); err != nil {
				return err
			}
		}
		icon, err := os.ReadFile(filepath.Join(project, "resources", "icons", "pencode.svg"))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(root, "usr", "share", "icons", "hicolor", "scalable", "apps", "pencode.svg"), icon, 0o644); err != nil {
			return err
		}
		return runPackaging("dpkg-deb", "--root-owner-group", "--build", root, destination)
	}
	return fmt.Errorf("unsupported installer platform %s", runtime.GOOS)
}
