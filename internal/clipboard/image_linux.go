package clipboard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func ReadImage(ctx context.Context) ([]byte, error) {
	tool := "xclip"
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		tool = "wl-paste"
	}
	path, err := exec.LookPath(tool)
	if err != nil {
		return nil, fmt.Errorf("reading clipboard images requires %s: %w", tool, err)
	}
	args := []string{"-selection", "clipboard", "-out", "-target", "TARGETS"}
	if tool == "wl-paste" {
		args = []string{"--list-types"}
	}
	types, err := clipboardCommand(ctx, path, args...)
	if err != nil {
		// Both tools exit with status 1 when there is no clipboard owner.
		var exit *exec.ExitError
		if ctx.Err() == nil && errors.As(err, &exit) && exit.ExitCode() == 1 {
			return nil, nil
		}
		return nil, err
	}
	if !hasPNGType(string(types)) {
		return nil, nil
	}
	args = []string{"-selection", "clipboard", "-out", "-target", "image/png"}
	if tool == "wl-paste" {
		args = []string{"--no-newline", "--type", "image/png"}
	}
	return clipboardCommand(ctx, path, args...)
}

func hasPNGType(types string) bool {
	for line := range strings.SplitSeq(types, "\n") {
		if strings.TrimSpace(line) == "image/png" {
			return true
		}
	}
	return false
}

// Bound output while reading it, including from a stalled or hostile owner.
func clipboardCommand(ctx context.Context, path string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.WaitDelay = 100 * time.Millisecond
	var output imageBuffer
	cmd.Stdout = &output
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return output.buffer.Bytes(), nil
}

type imageBuffer struct{ buffer bytes.Buffer }

func (b *imageBuffer) Write(data []byte) (int, error) {
	if len(data) > maxImageBytes-b.buffer.Len() {
		return 0, errImageTooLarge
	}
	return b.buffer.Write(data)
}
