package server

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/pencode-labs/pencode/protocol"
)

func TestStreamWindowAndWarmReadAllocations(t *testing.T) {
	const count = protocol.StreamWindow / protocol.ChunkSize
	s := &serviceStream{ready: make(chan *serviceChunk, count), free: make(chan *serviceChunk, count), done: make(chan struct{})}
	var chunks [count]serviceChunk
	e := protocol.Encoder{Buffer: make([]byte, 0, protocol.StreamWindow+8)}
	run := func() {
		e.Reset()
		for i := range chunks {
			chunks[i].n = protocol.ChunkSize
			chunks[i].data[0] = byte(i)
			s.ready <- &chunks[i]
		}
		if err := s.read(context.Background(), &e); err != nil {
			panic(err)
		}
		d := protocol.Decoder{Buffer: e.Buffer}
		ended, data := d.U32(), d.Bytes()
		if ended != 0 || len(data) != protocol.StreamWindow || d.Done() != nil {
			panic("stream window corrupted")
		}
		for i := range chunks {
			if data[i*protocol.ChunkSize] != byte(i) {
				panic("stream chunks reordered")
			}
			<-s.free
		}
	}
	run()
	if allocations := testing.AllocsPerRun(100, run); allocations != 0 {
		t.Fatalf("warm batched stream read allocated %g times", allocations)
	}
	// A single byte must be returned without waiting to fill the window.
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	chunks[0].n, chunks[0].data[0] = 1, 'x'
	s.ready <- &chunks[0]
	e.Reset()
	if err := s.read(ctx, &e); err != nil {
		t.Fatal("interactive read waited for more data", err)
	}
	<-s.free
	d := protocol.Decoder{Buffer: e.Buffer}
	if d.U32() != 0 || string(d.Bytes()) != "x" || d.Done() != nil {
		t.Fatal("lost interactive output")
	}
	// EOF may accompany the final bytes, including a partial UTF-8 sequence.
	chunks[0].n, chunks[0].data[0], chunks[0].err = 1, 0xf0, io.EOF
	s.ready <- &chunks[0]
	e.Reset()
	if err := s.read(ctx, &e); err != nil {
		t.Fatal(err)
	}
	<-s.free
	d = protocol.Decoder{Buffer: e.Buffer}
	if d.U32() != 1 || len(d.Bytes()) != 1 || d.Done() != nil {
		t.Fatal("lost final stream bytes")
	}
}
