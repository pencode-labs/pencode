package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/pencode-labs/pencode/internal/environment"
	"github.com/pencode-labs/pencode/internal/profiling"
	"github.com/pencode-labs/pencode/internal/systemproxy"
	"github.com/pencode-labs/pencode/internal/version"
	"github.com/pencode-labs/pencode/remote"
	"github.com/pencode-labs/pencode/server"
	"github.com/pencode-labs/pencode/transport"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(version.Value)
		return
	}
	environment.Configure()
	systemproxy.Configure()
	listen := flag.String("listen", "", "WebSocket listen address, e.g. 0.0.0.0:8080 (endpoint: /ws); empty uses stdio")
	token := flag.String("token", "", "Authentication token required with --listen")
	flag.Parse()
	if err := run(*listen, *token); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(address, token string) error {
	recorder, err := profiling.Start("server")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Pencode profiling:", err)
	}
	defer recorder.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if address == "" {
		if token != "" {
			return fmt.Errorf("--token requires --listen")
		}
		return server.Serve(ctx, transport.Stdio{}, server.Options{})
	}
	if err := remote.ValidateToken(token); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Pencode WebSocket listening on %s/ws\n", listener.Addr())
	return server.ServeWebSocket(ctx, listener, token, server.Options{})
}
