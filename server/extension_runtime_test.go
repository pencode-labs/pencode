package server

import (
	"bytes"
	"io"
	"testing"

	"github.com/pencode-labs/pencode/peb"
)

func TestExtensionSessionFramingWarmPathAllocatesNothing(t *testing.T) {
	r := extensionRuntime{writer: peb.Writer{Target: io.Discard}}
	s := extensionPipe{runtime: &r, id: 7}
	s.reader.Source = &s.source
	writer := peb.Writer{Target: &s}
	payload, _ := peb.EncodeHello(nil, peb.Hello{Host: "test", Capabilities: peb.Capabilities})
	if err := writer.Write(peb.Initialize, 1, payload); err != nil {
		t.Fatal(err)
	}
	if allocations := testing.AllocsPerRun(100, func() {
		if err := writer.Write(peb.Initialize, 1, payload); err != nil {
			panic(err)
		}
	}); allocations != 0 {
		t.Fatalf("cached session framing allocated %g times", allocations)
	}
}

func TestExtensionBufferWrapAndOverflow(t *testing.T) {
	var buffer extensionBuffer
	buffer.ready.L = &buffer.mu
	input := bytes.Repeat([]byte("0123456789"), 600)
	if _, err := buffer.Write(input); err != nil {
		t.Fatal(err)
	}
	first := make([]byte, 5000)
	_, _ = io.ReadFull(&buffer, first)
	if !bytes.Equal(first, input[:len(first)]) {
		t.Fatal("initial bytes changed")
	}
	_, _ = buffer.Write(input[:4000])
	rest := make([]byte, 5000)
	_, _ = io.ReadFull(&buffer, rest)
	if !bytes.Equal(rest, append(bytes.Clone(input[5000:]), input[:4000]...)) {
		t.Fatal("wrapped bytes changed")
	}
	if _, err := buffer.Write(make([]byte, extensionBacklog+1)); err != io.ErrShortBuffer {
		t.Fatal("unbounded backlog", err)
	}
	buffer.finish(io.EOF)
	if _, err := buffer.Read(rest); err != io.EOF {
		t.Fatal(err)
	}
}

func TestExtensionSessionDoesNotConsumeProcessSlots(t *testing.T) {
	s := services{streams: map[uint64]*serviceStream{
		1: {process: &extensionPipe{}},
		2: {process: readOnlyStream{ReadCloser: &extensionLogReader{}}},
		3: {process: readOnlyStream{ReadCloser: io.NopCloser(bytes.NewReader(nil))}},
	}}
	if got := s.processSlots(); got != 1 {
		t.Fatalf("physical process slots = %d", got)
	}
}
