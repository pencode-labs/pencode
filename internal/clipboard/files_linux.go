package clipboard

import (
	"context"
	"os"
	"os/exec"
	"time"
)

func HasFiles() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	files, _ := ReadFiles(ctx)
	return len(files) != 0
}

func ReadFiles(ctx context.Context) ([]string, error) {
	tool := "xclip"
	args := []string{"-selection", "clipboard", "-out", "-target", "text/uri-list"}
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		tool = "wl-paste"
		args = []string{"--no-newline", "--type", "text/uri-list"}
	}
	path, err := exec.LookPath(tool)
	if err != nil {
		return nil, err
	}
	data, err := clipboardCommand(ctx, path, args...)
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 && ctx.Err() == nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return fileURIs(string(data)), nil
}
