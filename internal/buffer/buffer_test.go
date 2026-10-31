package buffer

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestReadAllSizeHintsAndLimits(t *testing.T) {
	b := Get(0)
	defer Put(b)
	for _, test := range []struct {
		text  string
		size  int
		limit int
		err   error
	}{
		{"", 0, 0, nil},
		{"short", 20, 32, nil},
		{strings.Repeat("a", 4096), 1, 4096, nil},
		{strings.Repeat("b", 4097), 4096, 4096, ErrLimit},
		{"next", 0, 10, nil},
	} {
		err := b.ReadAll(strings.NewReader(test.text), test.size, test.limit)
		if !errors.Is(err, test.err) || string(b.Bytes) != test.text {
			t.Fatalf("hint=%d limit=%d: bytes=%d error=%v", test.size, test.limit, len(b.Bytes), err)
		}
	}
	failure := errors.New("read failed")
	if err := b.ReadAll(io.MultiReader(strings.NewReader("partial"), failingReader{failure}), 20, 32); !errors.Is(err, failure) || string(b.Bytes) != "partial" {
		t.Fatal(string(b.Bytes), err)
	}
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func TestWarmBufferReuse(t *testing.T) {
	for _, size := range sizes {
		b := Get(size)
		copy(b.Bytes, "kept")
		b.Resize(size / 2)
		b.Resize(size)
		if string(b.Bytes[:4]) != "kept" {
			t.Fatal("resize lost data")
		}
		Put(b)
		for range 100 {
			b := Get(size)
			if len(b.Bytes) != size || cap(b.Bytes) > size {
				t.Fatalf("unexpected buffer size %d/%d for %d", len(b.Bytes), cap(b.Bytes), size)
			}
			b.Bytes[0] = 1
			Put(b)
		}
	}
	b := Get(4096)
	defer Put(b)
	data := bytes.Repeat([]byte("x"), 2048)
	r := bytes.NewReader(data)
	if n := testing.AllocsPerRun(100, func() {
		r.Reset(data)
		if err := b.ReadAll(r, len(data), len(data)); err != nil {
			panic(err)
		}
	}); n != 0 {
		t.Fatalf("warm file read allocated %g times", n)
	}
}

func BenchmarkBufferReuse(b *testing.B) {
	Put(Get(32 << 10))
	for b.Loop() {
		value := Get(32 << 10)
		Put(value)
	}
}
