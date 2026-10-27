package peb

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"reflect"
	"testing"
)

type fragmented struct{ bytes.Buffer }

func (b *fragmented) Read(p []byte) (int, error)  { return b.Buffer.Read(p[:min(3, len(p))]) }
func (b *fragmented) Write(p []byte) (int, error) { return b.Buffer.Write(p[:min(2, len(p))]) }

func TestGoldenWireAndFragmentedIO(t *testing.T) {
	payload, err := EncodeHello(nil, Hello{Host: "pencode", Capabilities: Capabilities})
	if err != nil {
		t.Fatal(err)
	}
	var wire fragmented
	w := Writer{Target: &wire}
	if err := w.Write(Initialize, 0x12345678, payload); err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(wire.Bytes()); got != "706562000a000100785634120f0000000700000070656e636f6465ff010000" {
		t.Fatalf("wire format changed: %s", got)
	}
	if err := w.Write(Shutdown, 0, nil); err != nil {
		t.Fatal(err)
	}
	r := Reader{Source: &wire}
	f, err := r.Read()
	if err != nil || f.Kind != Initialize || f.ID != 0x12345678 {
		t.Fatalf("frame: %+v, %v", f, err)
	}
	hello, err := DecodeHello(f.Payload)
	if err != nil || hello != (Hello{Host: "pencode", Capabilities: Capabilities}) {
		t.Fatalf("hello: %+v, %v", hello, err)
	}
	f, err = r.Read()
	if err != nil || f.Kind != Shutdown || len(f.Payload) != 0 {
		t.Fatalf("shutdown: %+v, %v", f, err)
	}
	if _, err := r.Read(); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}

func TestContributionsOwnStringsAndRejectMalformedPayloads(t *testing.T) {
	want := Contributions{
		Fonts:         []Font{{"mono", "等宽", "resources/fonts/mono.ttf", "resources/fonts/bold.ttf"}},
		Themes:        []Theme{{"ink", "墨色", "resources/themes/ink.json"}},
		IconThemes:    []IconTheme{{"code", "代码", []IconRule{{FileSuffix, ".d.ts", "resources/icons/code.svg"}, {DefaultFolder, "", "resources/icons/folder.svg"}}}},
		DebugAdapters: []DebugAdapter{{ID: "Delve", Label: "Go", Command: "bin/go", Args: []string{"--tool", "dlv"}}},
	}
	data, err := EncodeContributions(nil, want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeContributions(data)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip: %+v, %v", got, err)
	}
	for n := range len(data) {
		if _, err := DecodeContributions(data[:n]); err == nil {
			t.Fatalf("accepted truncated payload at %d", n)
		}
	}
	if _, err := DecodeContributions(append(bytes.Clone(data), 0)); err == nil {
		t.Fatal("accepted trailing bytes")
	}
	clear(data)
	if !reflect.DeepEqual(got, want) {
		t.Fatal("decoded strings alias the wire buffer")
	}
	for _, malformed := range [][]byte{
		{0xff, 0xff, 0xff, 0xff},
		{1, 0, 0, 0, 0xff},
		{1, 0, 0, 0, 0xff, 0, 0, 0},
	} {
		if _, err := DecodeContributions(malformed); err == nil {
			t.Fatalf("accepted %x", malformed)
		}
	}
	if _, err := EncodeFailure(nil, string([]byte{0xff})); err == nil {
		t.Fatal("encoded invalid UTF-8")
	}
	if _, err := DecodeFailure([]byte{1, 0, 0, 0, 0xff}); err == nil {
		t.Fatal("decoded invalid UTF-8")
	}
}

func TestRejectInvalidFrames(t *testing.T) {
	var valid bytes.Buffer
	w := Writer{Target: &valid}
	if err := w.Write(Register, 1, []byte{0, 0, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	for name, corrupt := range map[string]func([]byte){
		"magic":         func(b []byte) { b[0] = 'x' },
		"version":       func(b []byte) { b[4]++ },
		"kind":          func(b []byte) { b[6] = 99 },
		"id":            func(b []byte) { b[8] = 0 },
		"oversize":      func(b []byte) { binary.LittleEndian.PutUint32(b[12:], MaxPayload+1) },
		"shutdown body": func(b []byte) { b[6], b[8] = byte(Shutdown), 0 },
	} {
		t.Run(name, func(t *testing.T) {
			data := bytes.Clone(valid.Bytes())
			corrupt(data)
			r := Reader{Source: bytes.NewReader(data)}
			if _, err := r.Read(); !errors.Is(err, ErrProtocol) {
				t.Fatalf("invalid frame: %v", err)
			}
		})
	}
	for _, n := range []int{1, HeaderSize - 1, HeaderSize, valid.Len() - 1} {
		r := Reader{Source: bytes.NewReader(valid.Bytes()[:n])}
		if _, err := r.Read(); err == nil {
			t.Fatalf("accepted truncated frame at %d", n)
		}
	}
	if err := w.Write(Shutdown, 1, nil); !errors.Is(err, ErrProtocol) {
		t.Fatal(err)
	}
}

func TestServeHandshakeAndCapabilityFailure(t *testing.T) {
	for _, caps := range []Capability{Capabilities, FileIcons, EditorThemes} {
		var input, output bytes.Buffer
		w := Writer{Target: &input}
		hello, _ := EncodeHello(nil, Hello{"pencode", caps})
		_ = w.Write(Initialize, 42, hello)
		_ = w.Write(Shutdown, 0, nil)
		if err := Serve(&input, &output, Contributions{Themes: []Theme{{"ink", "Ink", "resources/themes/ink.json"}}, Fonts: []Font{{ID: "mono", Label: "Mono", Path: "resources/fonts/mono.ttf"}}}); err != nil {
			t.Fatal(err)
		}
		r := Reader{Source: &output}
		frame, err := r.Read()
		if err != nil || frame.ID != 42 {
			t.Fatalf("reply: %+v, %v", frame, err)
		}
		if caps == Capabilities && frame.Kind != Register || caps != Capabilities && frame.Kind != Failure {
			t.Fatal("capability negotiation failed")
		}
	}
}

func TestCachedFramingAndEncodingAllocateNothing(t *testing.T) {
	var wire bytes.Buffer
	w := Writer{Target: &wire}
	hello := Hello{"pencode", Capabilities}
	payload, _ := EncodeHello(nil, hello)
	_ = w.Write(Initialize, 1, payload)
	data := bytes.Clone(wire.Bytes())
	source := bytes.NewReader(data)
	r := Reader{Source: source}
	_, _ = r.Read()
	if allocations := testing.AllocsPerRun(100, func() {
		payload, _ = EncodeHello(payload, hello)
		wire.Reset()
		_ = w.Write(Initialize, 1, payload)
		source.Reset(data)
		_, _ = r.Read()
	}); allocations != 0 {
		t.Fatalf("cached codec allocated %g times", allocations)
	}
}

func FuzzDecode(f *testing.F) {
	seed, _ := EncodeContributions(nil, Contributions{IconThemes: []IconTheme{{ID: "code", Label: "Code", Rules: []IconRule{{FileSuffix, ".go", "resources/icons/code.svg"}}}}})
	f.Add(seed)
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = DecodeHello(data)
		_, _ = DecodeContributions(data)
		_, _ = DecodeFailure(data)
	})
}
