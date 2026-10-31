// Package extensionbuild keeps locally built extensions on the host's PEB SDK.
package extensionbuild

import (
	"os"
	"path/filepath"
	"strconv"
)

// Environment creates a temporary workspace without changing either go.mod.
// The caller owns directory and removes it after the Go command finishes.
func Environment(directory, sdk, extension string) ([]string, error) {
	sdk, err := filepath.Abs(sdk)
	if err != nil {
		return nil, err
	}
	extension, err = filepath.Abs(extension)
	if err != nil {
		return nil, err
	}
	workspace, err := filepath.Abs(filepath.Join(directory, "go.work"))
	if err != nil {
		return nil, err
	}
	data := "go 1.27.0\n\nuse (\n" + strconv.Quote(filepath.ToSlash(sdk)) + "\n" + strconv.Quote(filepath.ToSlash(extension)) + "\n)\n"
	if err := os.WriteFile(workspace, []byte(data), 0o600); err != nil {
		return nil, err
	}
	return append(os.Environ(), "GOWORK="+workspace), nil
}
