package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"

	"github.com/pencode-labs/pencode/remote"
	"github.com/pencode-labs/pencode/server"
	"github.com/pencode-labs/pencode/transport"
)

func main() {
	listen := flag.String("listen", "", "WebSocket listen address, e.g. 0.0.0.0:8080 (endpoint: /ws); empty uses stdio")
	token := flag.String("token", "", "Authentication token required with --listen")
	flag.Parse()
	if err := run(*listen, *token); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(address, token string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
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
