package client

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/pencode-labs/pencode/internal/buffer"
	"github.com/pencode-labs/pencode/protocol"
)

type signaledWrite struct {
	net.Conn
	once    sync.Once
	entered chan struct{}
}

func (s *signaledWrite) Write(p []byte) (int, error) {
	s.once.Do(func() { close(s.entered) })
	return s.Conn.Write(p)
}

func TestCancelledRequestRetainsUploadUntilWritten(t *testing.T) {
	a, peer := net.Pipe()
	defer peer.Close()
	conn := &signaledWrite{Conn: a, entered: make(chan struct{})}
	c := &Client{conn: conn, pending: make(map[uint64]*call), queue: make(chan *call, maxPending), cancels: make(chan uint64, maxPending), done: make(chan struct{})}
	c.stopped.Add(1)
	go c.write()
	defer c.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	payload := bytes.Repeat([]byte("original"), 512)
	want := bytes.Clone(payload)
	result := make(chan error, 1)
	go func() { _, err := c.request(ctx, protocol.Service, payload); result <- err }()
	select {
	case <-conn.entered:
	case <-time.After(time.Second):
		t.Fatal("upload did not start")
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	clear(payload)
	// Other requests may immediately reuse a returned buffer after cancellation.
	for range 32 {
		other := buffer.Get(len(want))
		clear(other.Bytes)
		buffer.Put(other)
	}
	r := protocol.Reader{Source: peer}
	frame, err := r.Read()
	if err != nil || frame.Kind != protocol.Service || !bytes.Equal(frame.Payload, want) {
		t.Fatal("cancelled in-flight upload was recycled early", err)
	}
	c.Close()
	if c.uploadBytes != 0 {
		t.Fatal("closed writer retained upload budget")
	}
}

func BenchmarkRequestPayloadReuse(b *testing.B) {
	for _, size := range []int{4 << 10, 256 << 10} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			a, peer := net.Pipe()
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				r := protocol.Reader{Source: peer}
				w := protocol.Writer{Target: peer}
				for {
					frame, err := r.Read()
					if err != nil {
						return
					}
					if frame.Kind != protocol.Data {
						if w.Write(protocol.Reply, frame.ID, nil) != nil {
							return
						}
					}
				}
			}()
			c, err := New(b.Context(), a)
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { c.Close(); peer.Close(); <-finished })
			payload := make([]byte, size)
			for range 8 {
				if _, err := c.request(b.Context(), protocol.Service, payload); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := c.request(b.Context(), protocol.Service, payload); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
