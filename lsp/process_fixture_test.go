package lsp

import (
	"context"
	"github.com/diiyw/onix/projects/pencode/transport"
	"io"
)

func startLocal(ctx context.Context, command string, args []string, options Options) (*Client, error) {
	conn, err := transport.StartWithOutput(options.Root, command, io.Discard, args...)
	if err != nil {
		return nil, err
	}
	return Connect(ctx, options, conn)
}
