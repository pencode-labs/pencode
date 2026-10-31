package lsp

import (
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
)

func BenchmarkDocumentSync(b *testing.B) {
	for _, test := range []struct {
		name string
		kind int
		size int
	}{{"incremental", 2, 64 << 10}, {"full", 1, 64 << 10}, {"full", 1, 2 << 20}} {
		b.Run(test.name+"/"+strconv.Itoa(test.size), func(b *testing.B) {
			local, peer := net.Pipe()
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				_, _ = io.Copy(io.Discard, peer)
			}()
			conn := NewConn(local, local, func() { local.Close() }, nil)
			b.Cleanup(func() { conn.Close(); peer.Close(); <-finished })
			c := &Client{Conn: conn, documents: make(map[URI]document), syncKind: test.kind, Encoding: UTF16}
			text := strings.Repeat("value\n", test.size/6)
			views := [2]string{text + "a", text + "b"}
			version := 0
			sync := func() {
				version++
				if err := c.SyncDocument(b.Context(), "file:///tmp/test.go", "go", views[version%2], version); err != nil {
					b.Fatal(err)
				}
			}
			for range 128 {
				sync()
			}
			if err := c.Flush(b.Context()); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				sync()
			}
		})
	}
}
