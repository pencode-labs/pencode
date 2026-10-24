// pencode-build-servers prepares standalone artifacts for offline SSH bootstrap.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	platforms := flag.String("platforms", "linux/amd64,linux/arm64,darwin/amd64,darwin/arm64", "Comma-separated SSH server platforms")
	output := flag.String("output", "resources/servers", "Artifact directory")
	flag.Parse()
	if err := build(*platforms, *output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func build(platforms, output string) error {
	baseEnv := make([]string, 0, len(os.Environ())+3)
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if key != "GOOS" && key != "GOARCH" && key != "CGO_ENABLED" {
			baseEnv = append(baseEnv, value)
		}
	}
	for _, platform := range strings.Split(platforms, ",") {
		goos, arch, ok := strings.Cut(platform, "/")
		if !ok || goos != "linux" && goos != "darwin" || arch != "amd64" && arch != "arm64" {
			return fmt.Errorf("unsupported platform %q", platform)
		}
		directory := filepath.Join(output, goos+"-"+arch)
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return err
		}
		stage, err := os.MkdirTemp(directory, ".build-")
		if err != nil {
			return err
		}
		binary := filepath.Join(stage, "pencode-server")
		cmd := exec.Command("go", "build", "-p", "1", "-trimpath", "-ldflags=-s -w -buildid=", "-o", binary, "./cmd/pencode-server")
		cmd.Env = append(baseEnv[:len(baseEnv):len(baseEnv)], "GOOS="+goos, "GOARCH="+arch, "CGO_ENABLED=0")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		fmt.Println("Building", platform)
		err = cmd.Run()
		if err == nil {
			err = os.Rename(binary, filepath.Join(directory, "pencode-server"))
		}
		os.RemoveAll(stage)
		if err != nil {
			return err
		}
	}
	return nil
}
