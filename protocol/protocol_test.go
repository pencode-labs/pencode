package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

type fragments struct{ bytes.Buffer }

func (f *fragments) Write(p []byte) (int, error) { return f.Buffer.Write(p[:min(len(p), 3)]) }
func (f *fragments) Read(p []byte) (int, error)  { return f.Buffer.Read(p[:min(len(p), 2)]) }

func TestFramingAndBufferOwnership(t *testing.T) {
	var stream fragments
	w := Writer{Target: &stream}
	for _, text := range []string{"中文\x00hello", "second"} {
		if err := w.Write(DocumentChange, 77, []byte(text)); err != nil {
			t.Fatal(err)
		}
	}
	r := Reader{Source: &stream}
	first, err := r.Read()
	if err != nil || first.ID != 77 || string(first.Payload) != "中文\x00hello" {
		t.Fatal(first, err)
	}
	owned := string(first.Payload)
	second, err := r.Read()
	if err != nil || string(second.Payload) != "second" || owned != "中文\x00hello" {
		t.Fatal(second, err)
	}
	if _, err := r.Read(); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}

func TestRejectMalformedFrames(t *testing.T) {
	var stream bytes.Buffer
	w := Writer{Target: &stream}
	if err := w.Write(Hello, 1, nil); err != nil {
		t.Fatal(err)
	}
	header := append([]byte(nil), stream.Bytes()...)
	for _, mutate := range []func([]byte){
		func(p []byte) { p[0] = 'x' },
		func(p []byte) { binary.LittleEndian.PutUint16(p[4:], Version+1) },
		func(p []byte) { binary.LittleEndian.PutUint16(p[6:], uint16(Failure+1)) },
		func(p []byte) { binary.LittleEndian.PutUint64(p[8:], 0) },
		func(p []byte) { binary.LittleEndian.PutUint32(p[16:], 1) },
		func(p []byte) { binary.LittleEndian.PutUint32(p[20:], MaxPayload+1) },
	} {
		p := append([]byte(nil), header...)
		mutate(p)
		r := Reader{Source: bytes.NewReader(p)}
		if _, err := r.Read(); !errors.Is(err, ErrProtocol) {
			t.Fatal("accepted malformed header", err)
		}
	}
	r := Reader{Source: bytes.NewReader(header[:len(header)-1])}
	if _, err := r.Read(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(err)
	}
}

func TestWarmCodecDoesNotAllocate(t *testing.T) {
	var stream bytes.Buffer
	e := Encoder{Buffer: make([]byte, 0, 128)}
	w := Writer{Target: &stream}
	r := Reader{Source: &stream}
	run := func() {
		e.Reset()
		e.U64(42)
		e.String("中文 source")
		stream.Reset()
		if err := w.Write(DocumentChange, 3, e.Buffer); err != nil {
			panic(err)
		}
		f, err := r.Read()
		if err != nil {
			panic(err)
		}
		d := Decoder{Buffer: f.Payload}
		if d.U64() != 42 || string(d.Bytes()) != "中文 source" || d.Done() != nil {
			panic("bad codec")
		}
	}
	run()
	if n := testing.AllocsPerRun(100, run); n != 0 {
		t.Fatalf("warm codec allocated %g times", n)
	}
}

func TestPayloadTruncation(t *testing.T) {
	d := Decoder{Buffer: []byte{255, 255, 255, 255}}
	if d.String() != "" || !errors.Is(d.Done(), ErrProtocol) {
		t.Fatal("unbounded string was accepted")
	}
	d = Decoder{Buffer: []byte{0}}
	if !errors.Is(d.Done(), ErrProtocol) {
		t.Fatal("trailing data accepted")
	}
}
