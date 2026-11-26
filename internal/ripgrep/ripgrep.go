// Package ripgrep runs ripgrep searches.
package ripgrep

//go:generate go run generate.go

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

//go:embed LICENSE-MIT
var License string

var configureCommand = func(*exec.Cmd) {}

// Run streams stdout to read. An empty result is a successful search.
func Run(ctx context.Context, dir string, stdin io.Reader, args []string, read func(*bufio.Reader) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	name, err := executable()
	if err != nil {
		return fmt.Errorf("ripgrep: %w", err)
	}
	cmd := exec.CommandContext(ctx, name, append([]string{"--no-config", "--color=never"}, args...)...)
	configureCommand(cmd)
	cmd.Dir, cmd.Stdin = dir, stdin
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	readErr := read(bufio.NewReader(out))
	if readErr != nil {
		_ = cmd.Process.Kill()
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if readErr != nil {
		return readErr
	}
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
		return nil
	}
	if err != nil {
		return fmt.Errorf("ripgrep: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// Records consumes NUL-terminated output. The slice is valid until visit returns.
func Records(r *bufio.Reader, visit func([]byte) error) error {
	var long []byte
	for {
		part, err := r.ReadSlice(0)
		switch err {
		case bufio.ErrBufferFull:
			long = append(long, part...)
			continue
		case io.EOF:
			if len(long)+len(part) != 0 {
				return io.ErrUnexpectedEOF
			}
			return nil
		case nil:
		default:
			return err
		}
		record := part[:len(part)-1]
		if len(long) > 0 {
			long = append(long, record...)
			record = long
		}
		if err := visit(record); err != nil {
			return err
		}
		long = long[:0]
	}
}
