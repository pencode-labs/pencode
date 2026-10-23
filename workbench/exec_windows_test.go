//go:build windows

package workbench

import (
	"context"
	"fmt"
	"os"
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
	cmd := sourceCommand(ctx)
	if cmd.Err != nil {
		t.Fatal(cmd.Err)
	}
	// Run this test binary with the same process setup used by every Git path.
	cmd.Path, cmd.Args = executable, []string{executable, "-test.run=^TestSourceControlCommandWithoutConsole$"}
	cmd.Env = append(os.Environ(), helper+"=1")
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("console probe: %v: %s", err, data)
	}
	if strings.TrimSpace(string(data)) != "0" {
		t.Fatalf("background command acquired a console window: %s", data)
	}
}
