//go:build windows

package server

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestSourceControlCommandWithoutConsole(t *testing.T) {
	const helper = "ONIX_PENCODE_TEST_CONSOLE"
	if os.Getenv(helper) == "1" {
		window, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow").Call()
		fmt.Print(window)
		os.Exit(0)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestSourceControlCommandWithoutConsole$")
	hideServiceWindow(cmd)
	if cmd.Err != nil {
		t.Fatal(cmd.Err)
	}
	// Exercise the same process setup used by Git in the backend.
	cmd.Env = append(os.Environ(), helper+"=1")
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("console probe: %v: %s", err, data)
	}
	if strings.TrimSpace(string(data)) != "0" {
		t.Fatalf("background command acquired a console window: %s", data)
	}
}
