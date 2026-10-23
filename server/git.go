package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pencode-labs/pencode/protocol"
)

func command(ctx context.Context, root string, args []string) ([]byte, error) {
	if len(args) == 0 {
		return nil, protocol.ErrProtocol
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	argv := []string{"--no-optional-locks", "--literal-pathspecs", "-c", "core.quotePath=false", "-c", "color.ui=false", "-C", root}
	cmd := exec.CommandContext(ctx, "git", append(argv, args...)...)
	hideServiceWindow(cmd)
	cmd.WaitDelay = 200 * time.Millisecond
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat")
	var output, stderr limitedOutput
	cmd.Stdout, cmd.Stderr = &output, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("Git operation interrupted: %w", ctx.Err())
	}
	if err != nil {
		message := strings.TrimSpace(string(stderr.data))
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("git %s: %s", args[0], message)
	}
	return output.data, nil
}

type limitedOutput struct{ data []byte }

func (b *limitedOutput) Write(p []byte) (int, error) {
	if len(b.data)+len(p) > protocol.MaxMessage-1024 {
		return 0, errors.New("process output exceeds budget")
	}
	b.data = append(b.data, p...)
	return len(p), nil
}
func diffPatch(ctx context.Context, left, right []byte) ([]byte, error) {
	if bytes.Count(left, []byte{'\n'}) > 100000 || bytes.Count(right, []byte{'\n'}) > 100000 {
		return nil, errors.New("file exceeds the 100,000 line text comparison limit")
	}
	directory, err := os.MkdirTemp("", "pencode-diff-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)
	lp, rp := filepath.Join(directory, "left"), filepath.Join(directory, "right")
	if err = os.WriteFile(lp, left, 0600); err != nil {
		return nil, err
	}
	if err = os.WriteFile(rp, right, 0600); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "diff", "--no-index", "--no-ext-diff", "--no-textconv", "--no-color", "--text", "--unified=0", "--", lp, rp)
	hideServiceWindow(cmd)
	cmd.WaitDelay = 200 * time.Millisecond
	var out limitedOutput
	cmd.Stdout = &out
	err = cmd.Run()
	var exit *exec.ExitError
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1) {
		return nil, err
	}
	return out.data, nil
}

func (s *services) git(ctx context.Context, root string, d *protocol.Decoder, e *protocol.Encoder) error {
	count := d.U32()
	if count > 4096 {
		return protocol.ErrProtocol
	}
	args := make([]string, count)
	for i := range args {
		args[i] = d.String()
	}
	if err := d.Done(); err != nil {
		return err
	}
	data, err := command(ctx, root, args)
	if err != nil {
		return err
	}
	if len(args) == 2 && args[0] == "rev-parse" && args[1] == "--show-toplevel" {
		s.mu.Lock()
		discovered := strings.TrimSuffix(string(data), "\n")
		if s.roots[root] {
			s.roots[discovered] = true
			s.derived[discovered] = root
		}
		s.mu.Unlock()
	}
	e.Bytes(data)
	return nil
}
func (s *services) diff(ctx context.Context, d *protocol.Decoder, e *protocol.Encoder) error {
	left, right := d.Bytes(), d.Bytes()
	if err := d.Done(); err != nil {
		return err
	}
	patch, err := diffPatch(ctx, left, right)
	if err != nil {
		return err
	}
	e.Bytes(patch)
	return nil
}
