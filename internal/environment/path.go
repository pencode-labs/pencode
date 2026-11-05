// Package environment prepares process environment inherited by workspace
// tools when Pencode is launched from a desktop rather than a shell.
package environment

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	pathStart = "__PENCODE_PATH_START__"
	pathEnd   = "__PENCODE_PATH_END__"
)

// Configure imports PATH from the user's interactive login shell. Desktop
// launchers commonly omit shell profile directories such as Homebrew's bin.
// A failed shell probe leaves the inherited environment unchanged.
func Configure() {
	if runtime.GOOS == "windows" {
		return
	}
	path := loginPATH(os.Getenv("SHELL"))
	if path != "" {
		_ = os.Setenv("PATH", path)
	}
}

func loginPATH(shell string) string {
	if shell == "" {
		if runtime.GOOS == "darwin" {
			shell = "/bin/zsh"
		} else {
			shell = "/bin/sh"
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	command := fmt.Sprintf("printf '%s%%s%s' \"$PATH\"", pathStart, pathEnd)
	output, err := exec.CommandContext(ctx, shell, "-ilc", command).Output()
	if err != nil {
		return ""
	}
	_, value, ok := strings.Cut(string(output), pathStart)
	if !ok {
		return ""
	}
	value, _, ok = strings.Cut(value, pathEnd)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}
