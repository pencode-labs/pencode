package server

import (
	"context"
	"io"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/pencode-labs/pencode/protocol"
)

type observedStreamReader struct {
	*io.PipeReader
	entered chan struct{}
	once    sync.Once
}

func (p *observedStreamReader) Read(data []byte) (int, error) {
	p.once.Do(func() { close(p.entered) })
	return p.PipeReader.Read(data)
}

func TestIdleServiceStreamsMemory(t *testing.T) {
	ctx := t.Context()
	runtime.GC()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	var streams [16]*serviceStream
	for i := range streams {
		reader, writer := io.Pipe()
		p := &observedStreamReader{PipeReader: reader, entered: make(chan struct{})}
		s := newServiceStream(ctx, readOnlyStream{p}, nil)
		streams[i] = s
		t.Cleanup(func() { s.close(); writer.Close() })
		select {
		case <-p.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("stream reader did not start")
		}
	}
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&after)
	used := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	if used > 1<<20 {
		t.Fatalf("16 idle streams retain %d bytes, budget 1 MiB", used)
	}
	t.Logf("16 idle streams retain %.3f MiB", float64(used)/(1<<20))
	runtime.KeepAlive(streams)
}

func TestServiceStreamBacklogGrowsToBoundedWindow(t *testing.T) {
	reader, writer := io.Pipe()
	s := newServiceStream(t.Context(), readOnlyStream{reader}, nil)
	t.Cleanup(func() { s.close(); writer.Close() })
	const chunks = protocol.StreamWindow / protocol.ChunkSize
	data := make([]byte, (chunks+1)*protocol.ChunkSize)
	for i := range data {
		data[i] = byte(i / protocol.ChunkSize)
	}
	written := make(chan error, 1)
	go func() {
		_, err := writer.Write(data)
		writer.Close()
		written <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for len(s.ready) != chunks {
		if time.Now().After(deadline) {
			t.Fatal("stream did not grow to the full transfer window")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-written:
		t.Fatalf("producer passed the bounded window without a consumer: %v", err)
	default:
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var e protocol.Encoder
	offset := 0
	for {
		e.Reset()
		if err := s.read(ctx, &e); err != nil {
			t.Fatal(err)
		}
		d := protocol.Decoder{Buffer: e.Buffer}
		ended, payload := d.U32(), d.Bytes()
		if d.Done() != nil || len(payload) > protocol.StreamWindow {
			t.Fatal("invalid stream frame")
		}
		for _, value := range payload {
			if offset >= len(data) || value != data[offset] {
				t.Fatalf("stream data reordered at %d", offset)
			}
			offset++
		}
		if ended != 0 {
			break
		}
	}
	if offset != len(data) {
		t.Fatalf("received %d bytes, want %d", offset, len(data))
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
}

func TestServiceStreamCancellationReleasesBlockedReads(t *testing.T) {
	for _, backlog := range []bool{false, true} {
		name := "idle"
		if backlog {
			name = "full-window"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			reader, writer := io.Pipe()
			s := newServiceStream(ctx, readOnlyStream{reader}, nil)
			t.Cleanup(func() { s.close(); writer.Close() })
			if backlog {
				written := make(chan struct{})
				go func() {
					defer close(written)
					_, _ = writer.Write(make([]byte, protocol.StreamWindow+protocol.ChunkSize))
				}()
				deadline := time.Now().Add(5 * time.Second)
				for len(s.ready) != cap(s.ready) {
					if time.Now().After(deadline) {
						t.Fatal("stream did not fill its window")
					}
					time.Sleep(time.Millisecond)
				}
				t.Cleanup(func() { <-written })
			}
			cancel()
			select {
			case <-s.readerDone:
			case <-time.After(5 * time.Second):
				t.Fatal("cancellation left a blocked stream reader")
			}
		})
	}
}

func BenchmarkServiceStreamInteractive(b *testing.B) {
	benchmarkServiceStream(b, 1)
}

func BenchmarkServiceStreamBulk(b *testing.B) {
	benchmarkServiceStream(b, protocol.ChunkSize)
}

func benchmarkServiceStream(b *testing.B, size int) {
	reader, writer := io.Pipe()
	s := newServiceStream(b.Context(), readOnlyStream{reader}, nil)
	requests := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		data := make([]byte, size)
		for range requests {
			if _, err := writer.Write(data); err != nil {
				return
			}
		}
	}()
	b.Cleanup(func() { close(requests); s.close(); writer.Close(); <-finished })
	e := protocol.Encoder{Buffer: make([]byte, 0, protocol.StreamWindow+8)}
	ctx := b.Context()
	for range 16 {
		requests <- struct{}{}
		e.Reset()
		if err := s.read(ctx, &e); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.SetBytes(int64(size))
	for b.Loop() {
		requests <- struct{}{}
		e.Reset()
		if err := s.read(ctx, &e); err != nil {
			b.Fatal(err)
		}
	}
}
