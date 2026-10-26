package client

import (
	"context"
	"io"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pencode-labs/pencode/protocol"
)

func TestDebugTerminalPreservesArgvEnvironmentAndInput(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("fixture uses a POSIX shell")
	}
	c, w := peer(t, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	t.Setenv("PENCODE_DEBUG_REMOVE", "inherited")
	value := "env 中文"
	literal := "$(printf WRONG); literal ' argument"
	p, pid, err := c.OpenDebugTerminal(ctx, w.Path, protocol.TerminalCommand{Directory: w.Path, Args: []string{"/bin/sh", "-c", `printf 'ARG:%s\nENV:%s\nREMOVE:%s\n' "$1" "$PENCODE_DEBUG_VALUE" "${PENCODE_DEBUG_REMOVE-unset}"; IFS= read -r line; printf 'INPUT:%s\n' "$line"`, "fixture", literal}, Env: map[string]*string{"PENCODE_DEBUG_VALUE": &value, "PENCODE_DEBUG_REMOVE": nil}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if pid <= 0 {
		t.Fatal("missing target PID")
	}
	if _, err := p.Write([]byte("hello terminal\n")); err != nil {
		t.Fatal(err)
	}
	result := make(chan string, 1)
	go func() { data, _ := io.ReadAll(p); result <- strings.ReplaceAll(string(data), "\r\n", "\n") }()
	select {
	case data := <-result:
		for _, expected := range []string{"ARG:" + literal, "ENV:" + value, "REMOVE:unset", "INPUT:hello terminal"} {
			if !strings.Contains(data, expected) {
				t.Fatalf("missing %q in %q", expected, data)
			}
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := p.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestDebugTCPStreamRevokedWithWorkspace(t *testing.T) {
	c, w := peer(t, nil)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	p, err := c.OpenDebugAdapter(ctx, w.Path, "tcp", "", listener.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var conn net.Conn
	select {
	case conn = <-accepted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	defer conn.Close()
	if err := c.CloseWorkspace(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	var buf [1]byte
	if _, err := conn.Read(buf[:]); err != io.EOF {
		t.Fatalf("workspace close retained adapter socket: %v", err)
	}
}
