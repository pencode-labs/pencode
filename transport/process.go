// Package transport supplies owned byte streams independently of the CS API.
package transport

import (
	"context"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

type process struct {
	io.ReadCloser
	writer   io.WriteCloser
	cmd      *exec.Cmd
	once     sync.Once
	done     chan struct{}
	err      error
	graceful bool
}

// Start launches a protocol peer without a terminal. stdout carries frames and
// stderr remains diagnostic output. Close always reaps the process.
func Start(executable string, args ...string) (io.ReadWriteCloser, error) {
	return StartPeerWithOutput(executable, os.Stderr, args...)
}

// StartPeerWithOutput owns a protocol peer, including graceful EOF shutdown.
func StartPeerWithOutput(executable string, stderr io.Writer, args ...string) (io.ReadWriteCloser, error) {
	return StartPeerWithEnv(executable, stderr, nil, args...)
}

// StartPeerWithEnv supplies an environment only to this child process.
func StartPeerWithEnv(executable string, stderr io.Writer, env []string, args ...string) (io.ReadWriteCloser, error) {
	return startWithEnv("", executable, stderr, env, true, args...)
}

// StartPeerIn owns a protocol peer in its workspace, including EOF shutdown.
func StartPeerIn(directory, executable string, stderr io.Writer, args ...string) (io.ReadWriteCloser, error) {
	return startWithEnv(directory, executable, stderr, nil, true, args...)
}

// Run executes a bounded bootstrap command. Cancellation kills its process
// group so SSH proxy children cannot keep inherited descriptors open.
func Run(ctx context.Context, input io.Reader, output, stderr io.Writer, executable string, args ...string) error {
	cmd := exec.CommandContext(ctx, executable, args...)
	hideWindow(cmd)
	cmd.Cancel = func() error { return terminate(cmd) }
	cmd.WaitDelay = time.Second
	cmd.Stdin, cmd.Stdout, cmd.Stderr = input, output, stderr
	return cmd.Run()
}

// StartIn starts a headless process beside its workspace.
func StartIn(directory, executable string, args ...string) (io.ReadWriteCloser, error) {
	return StartWithOutput(directory, executable, os.Stderr, args...)
}

// StartWithOutput supplies a separate stderr sink; stdout stays protocol-only.
func StartWithOutput(directory, executable string, stderr io.Writer, args ...string) (io.ReadWriteCloser, error) {
	return startWithEnv(directory, executable, stderr, nil, false, args...)
}

func startWithEnv(directory, executable string, stderr io.Writer, env []string, graceful bool, args ...string) (io.ReadWriteCloser, error) {
	cmd := exec.Command(executable, args...)
	cmd.Env = env
	cmd.Dir = directory
	hideWindow(cmd)
	cmd.WaitDelay = time.Second
	cmd.Stderr = stderr
	childIn, in, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	out, childOut, err := os.Pipe()
	if err != nil {
		in.Close()
		childIn.Close()
		return nil, err
	}
	cmd.Stdin, cmd.Stdout = childIn, childOut
	err = cmd.Start()
	childIn.Close()
	childOut.Close()
	if err != nil {
		in.Close()
		out.Close()
		return nil, err
	}
	p := &process{ReadCloser: out, writer: in, cmd: cmd, done: make(chan struct{}), graceful: graceful}
	go func() { p.err = cmd.Wait(); close(p.done) }()
	return p, nil
}
func (p *process) Wait() error                 { <-p.done; return p.err }
func (p *process) Write(b []byte) (int, error) { return p.writer.Write(b) }
func (p *process) Close() error {
	p.once.Do(func() {
		_ = p.writer.Close()
		if p.graceful {
			// Keep stdout open while the server finishes in-flight replies. Closing it
			// first can deliver SIGPIPE before the server has reaped its children.
			drained := make(chan struct{})
			go func() { _, _ = io.Copy(io.Discard, p.ReadCloser); close(drained) }()
			timer := time.NewTimer(2 * time.Second)
			defer timer.Stop()
			select {
			case <-p.done:
			case <-timer.C:
				_ = terminate(p.cmd)
				<-p.done
			}
			_ = p.ReadCloser.Close()
			<-drained
		} else {
			_ = p.ReadCloser.Close()
			_ = terminate(p.cmd)
			<-p.done
		}
	})
	return nil
}

// Stdio is used by the standalone server and the GUI's headless child mode.
type Stdio struct{}

func (Stdio) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }
func (Stdio) Write(p []byte) (int, error) { return os.Stdout.Write(p) }
func (Stdio) Close() error                { _ = os.Stdin.Close(); return os.Stdout.Close() }
