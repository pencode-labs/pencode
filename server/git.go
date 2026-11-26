package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/pencode-labs/pencode/protocol"
)

func command(ctx context.Context, root string, args []string) ([]byte, error) {
	var out limitedOutput
	err := runGit(ctx, root, args, nil, &out)
	if err != nil {
		return nil, err
	}
	return out.data, nil
}

func runGit(ctx context.Context, root string, args []string, input []byte, output io.Writer) error {
	if len(args) == 0 {
		return protocol.ErrProtocol
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	argv := make([]string, 0, len(args)+8)
	argv = append(argv, "--no-optional-locks")
	if args[0] != "check-ignore" {
		argv = append(argv, "--literal-pathspecs")
	}
	argv = append(argv, "-c", "core.quotePath=false", "-c", "color.ui=false", "-C", root)
	cmd := exec.CommandContext(ctx, gitExecutable(), append(argv, args...)...)
	cmd.Args[0] = "git"
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	hideServiceWindow(cmd)
	cmd.WaitDelay = 200 * time.Millisecond
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat")
	if (args[0] == "status" || args[0] == "-c") && slices.Contains(args, "--porcelain=v1") {
		// The workbench uses stderr to identify a missing submodule worktree.
		cmd.Env = append(cmd.Env, "LC_ALL=C")
	}
	var stderr limitedOutput
	cmd.Stdout, cmd.Stderr = output, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return fmt.Errorf("Git operation interrupted: %w", ctx.Err())
	}
	var exit *exec.ExitError
	if input != nil && args[0] == "check-ignore" && errors.As(err, &exit) && exit.ExitCode() == 1 {
		err = nil // No paths matched an ignore rule.
	}
	if err != nil {
		message := strings.TrimSpace(string(stderr.data))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("git %s: %s", args[0], message)
	}
	return nil
}

func (s *services) gitCheckIgnore(ctx context.Context, root string, d *protocol.Decoder, e *protocol.Encoder) error {
	count := d.U32()
	if count == 0 || count > protocol.IndexPageSize {
		return protocol.ErrProtocol
	}
	// Git consumes stdin before returning, so the reply storage can hold paths
	// until it is reused for the much smaller ignore flags.
	input := e.Buffer[:0]
	for range count {
		name := d.Bytes()
		if len(name) == 0 || bytes.IndexByte(name, 0) >= 0 {
			return protocol.ErrProtocol
		}
		input = append(input, name...)
		input = append(input, 0)
	}
	if err := d.Done(); err != nil {
		return err
	}
	var matches [protocol.IndexPageSize]bool
	output := gitIgnoreOutput{matches: matches[:count]}
	if err := runGit(ctx, root, []string{"check-ignore", "--stdin", "-z", "--verbose", "--non-matching"}, input, &output); err != nil {
		return err
	}
	if output.field != int(count)*4 || output.partial || output.bytes > protocol.MaxMessage-1024 {
		return protocol.ErrProtocol
	}
	e.Buffer = input[:0]
	e.U32(count)
	for _, ignored := range output.matches {
		if ignored {
			e.U32(1)
		} else {
			e.U32(0)
		}
	}
	return nil
}

// Git writes four NUL-terminated fields per path. Only the first byte of the
// third field (the pattern) is needed, even when a field spans pipe reads.
type gitIgnoreOutput struct {
	matches      []bool
	field, bytes int
	partial      bool
}

func (out *gitIgnoreOutput) Write(data []byte) (int, error) {
	n := len(data)
	out.bytes += n
	if out.bytes > protocol.MaxMessage-1024 {
		return 0, errors.New("process output exceeds budget")
	}
	for len(data) != 0 {
		if out.field >= len(out.matches)*4 {
			out.partial = true
			return n - len(data), protocol.ErrProtocol
		}
		if !out.partial && out.field%4 == 2 {
			out.matches[out.field/4] = data[0] != 0 && data[0] != '!'
		}
		end := bytes.IndexByte(data, 0)
		out.partial = end < 0
		if out.partial {
			break
		}
		out.field++
		data = data[end+1:]
	}
	return n, nil
}

func (out *gitIgnoreOutput) ReadFrom(r io.Reader) (int64, error) {
	return readCommandOutput(out, r)
}

type limitedOutput struct{ data []byte }

var commandCopyBuffers = sync.Pool{New: func() any { return new([protocol.ChunkSize]byte) }}

// ReadFrom lets os.File.WriteTo reuse copy scratch instead of allocating a
// fresh buffer for each stdout/stderr pipe. Output always owns its bytes.
func (b *limitedOutput) ReadFrom(r io.Reader) (int64, error) {
	return readCommandOutput(b, r)
}

func readCommandOutput[W io.Writer](out W, r io.Reader) (int64, error) {
	buffer := commandCopyBuffers.Get().(*[protocol.ChunkSize]byte)
	defer commandCopyBuffers.Put(buffer)
	var total int64
	for {
		n, err := r.Read(buffer[:])
		if n > 0 {
			written, writeErr := out.Write(buffer[:n])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
		}
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			return total, err
		}
	}
}

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
