package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/diiyw/onix/projects/pencode/server"
	"github.com/diiyw/onix/projects/pencode/transport"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := server.Serve(ctx, transport.Stdio{}, server.Options{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
