// pencode-release uses Onix's resource compiler and desktop packager.
package main

import (
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/onix-engine/onix/editor"
	"github.com/pencode-labs/pencode/internal/systemproxy"
)

func main() {
	systemproxy.Configure()
	output := flag.String("output", "dist", "Distribution directory")
	version := flag.String("version", "dev", "Release version or tag")
	flag.Parse()
	if err := release(*output, *version); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func release(output, version string) error {
	if version == "" || strings.IndexFunc(version, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(".-_", r))
	}) >= 0 {
		return fmt.Errorf("invalid release version %q", version)
	}
	platform := editor.BuildPlatform(runtime.GOOS)
	if runtime.GOOS == "darwin" {
		platform = editor.BuildPlatformMacOS
	}
	if platform != editor.BuildPlatformLinux && platform != editor.BuildPlatformWindows && platform != editor.BuildPlatformMacOS {
		return fmt.Errorf("unsupported desktop platform %s", runtime.GOOS)
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return fmt.Errorf("unsupported desktop architecture %s", runtime.GOARCH)
	}
	project, err := os.Getwd()
	if err != nil {
		return err
	}
	artifact, err := editor.BuildProject(editor.BuildOptions{
		ProjectDir: project, Platform: platform, Mode: editor.BuildModeRelease,
		Architecture: editor.BuildArchitecture(runtime.GOARCH), AppName: "pencode",
		Progress: func(p editor.BuildProgress) { fmt.Fprintln(os.Stderr, p.Message) },
	})
	if err != nil {
		return err
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(output, 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(output, ".pencode-installer-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := stageDistribution(stage, artifact, project, version); err != nil {
		return err
	}
	name := fmt.Sprintf("pencode-%s-%s-%s%s", version, runtime.GOOS, runtime.GOARCH, installerExtension(runtime.GOOS))
	destination := filepath.Join(output, name)
	built := filepath.Join(stage, name)
	if err := buildInstaller(stage, built, project, version); err != nil {
		return err
	}
	if err := os.Rename(built, destination); err != nil {
		return err
	}
	file, err := os.Open(destination)
	if err != nil {
		return err
	}
	digest := sha256.New()
	_, err = io.Copy(digest, file)
	if err := errors.Join(err, file.Close()); err != nil {
		return err
	}
	if err := os.WriteFile(destination+".sha256", fmt.Appendf(nil, "%x  %s\n", digest.Sum(nil), name), 0o644); err != nil {
		return err
	}
	fmt.Println(destination)
	return nil
}
