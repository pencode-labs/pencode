package server

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/pencode-labs/pencode/protocol"
)

type outputTestReader struct {
	err  error
	data []byte
}

func (r *outputTestReader) Read(p []byte) (int, error) {
	n := copy(p, r.data)
	r.data = r.data[n:]
	if len(r.data) == 0 {
		return n, r.err
	}
	return n, nil
}

func TestLimitedOutputReadFromOwnsDataAndPreservesErrors(t *testing.T) {
	wantErr := errors.New("read failed")
	for _, readErr := range []error{io.EOF, wantErr} {
		source := &outputTestReader{data: []byte("output"), err: readErr}
		var output limitedOutput
		n, err := output.ReadFrom(source)
		if readErr == io.EOF {
			readErr = nil
		}
		if n != 6 || err != readErr || string(output.data) != "output" {
			t.Fatalf("read = %d, %v, %q", n, err, output.data)
		}
		var other limitedOutput
		if _, err := other.ReadFrom(bytes.NewReader(bytes.Repeat([]byte{'x'}, protocol.ChunkSize))); err != nil {
			t.Fatal(err)
		}
		if string(output.data) != "output" {
			t.Fatal("reusing copy scratch overwrote a previous command's output")
		}
	}
}

func TestLimitedOutputReadFromEnforcesBudget(t *testing.T) {
	const limit = protocol.MaxMessage - 1024
	output := limitedOutput{data: make([]byte, limit-1, limit)}
	if n, err := output.ReadFrom(bytes.NewReader([]byte{'x'})); err != nil || n != 1 {
		t.Fatalf("exact budget = %d, %v", n, err)
	}
	if n, err := output.ReadFrom(bytes.NewReader([]byte{'y'})); err == nil || n != 0 || len(output.data) != limit || output.data[limit-1] != 'x' {
		t.Fatalf("excess output = %d, %v, size %d", n, err, len(output.data))
	}
}

func BenchmarkLimitedOutputCopy(b *testing.B) {
	payload := bytes.Repeat([]byte("file.go\n"), 128)
	reader := bytes.NewReader(payload)
	// os.File.WriteTo uses the destination's ReaderFrom through io.Copy.
	source := struct{ io.Reader }{reader}
	output := limitedOutput{data: make([]byte, 0, len(payload))}
	if _, err := io.Copy(&output, &source); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	for b.Loop() {
		reader.Reset(payload)
		output.data = output.data[:0]
		if n, err := io.Copy(&output, &source); err != nil || n != int64(len(payload)) {
			b.Fatalf("copy = %d, %v", n, err)
		}
	}
}

func BenchmarkGitCheckIgnore(b *testing.B) {
	if _, err := exec.LookPath("git"); err != nil {
		b.Skip("Git unavailable")
	}
	root := b.TempDir()
	if _, err := command(b.Context(), root, []string{"init", "-q"}); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("*.log\n"), 0600); err != nil {
		b.Fatal(err)
	}
	const paths = 128
	var request, reply protocol.Encoder
	request.U32(paths)
	for i := range paths {
		request.String(fmt.Sprintf("nested/file-%d.log", i))
	}
	var service services
	run := func() {
		reply.Reset()
		decoder := protocol.Decoder{Buffer: request.Buffer}
		if err := service.gitCheckIgnore(b.Context(), root, &decoder, &reply); err != nil {
			b.Fatal(err)
		}
		result := protocol.Decoder{Buffer: reply.Buffer}
		if result.U32() != paths {
			b.Fatal("wrong ignore count")
		}
		for range paths {
			if result.U32() != 1 {
				b.Fatal("ignored path was not marked")
			}
		}
		if err := result.Done(); err != nil {
			b.Fatal(err)
		}
	}
	run()
	run()
	b.ReportAllocs()
	for b.Loop() {
		run()
	}
}

func TestGitIgnoreOutputChunkBoundaries(t *testing.T) {
	data := []byte(".gitignore\x001\x00*.log\x00a.log\x00.gitignore\x002\x00!keep.log\x00keep.log\x00\x00\x00\x00main.go\x00")
	for split := 0; split <= len(data); split++ {
		var flags [3]bool
		out := gitIgnoreOutput{matches: flags[:]}
		for _, chunk := range [][]byte{data[:split], data[split:]} {
			if n, err := out.Write(chunk); err != nil || n != len(chunk) {
				t.Fatal(split, n, err)
			}
		}
		if flags != [3]bool{true, false, false} || out.field != 12 || out.partial {
			t.Fatalf("split %d: flags %v field %d", split, flags, out.field)
		}
		if _, err := out.Write([]byte{0}); !errors.Is(err, protocol.ErrProtocol) {
			t.Fatal("unexpected trailing field", err)
		}
	}
}
