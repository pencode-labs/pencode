// Package buffer reuses exclusively owned temporary byte storage.
package buffer

import (
	"errors"
	"io"
	"slices"
	"sync"
)

var sizes = [...]int{1 << 10, 2 << 10, 4 << 10, 8 << 10, 16 << 10, 32 << 10, 64 << 10, 128 << 10, 256 << 10, 512 << 10, 1 << 20}
var pools [len(sizes)]sync.Pool

var ErrLimit = errors.New("buffer: read exceeds limit")

// Buffer must not be accessed after Put or shared while being resized.
type Buffer struct{ Bytes []byte }

func Get(size int) *Buffer {
	for i, capacity := range sizes {
		if size <= capacity {
			if value := pools[i].Get(); value != nil {
				b := value.(*Buffer)
				b.Resize(size)
				return b
			}
			return &Buffer{Bytes: make([]byte, size, capacity)}
		}
	}
	return &Buffer{Bytes: make([]byte, size)}
}

func Put(b *Buffer) {
	if b == nil {
		return
	}
	for i, capacity := range sizes {
		if cap(b.Bytes) <= capacity {
			b.Bytes = b.Bytes[:0]
			pools[i].Put(b)
			return
		}
	}
}

func (b *Buffer) Resize(size int) {
	if size > cap(b.Bytes) {
		for _, tier := range sizes {
			if size <= tier {
				next := make([]byte, size, tier)
				copy(next, b.Bytes)
				b.Bytes = next
				return
			}
		}
		b.Bytes = slices.Grow(b.Bytes, size-len(b.Bytes))
	}
	b.Bytes = b.Bytes[:size]
}

// ReadAll uses a size hint, detects files growing beyond limit, and retains its
// storage for the next file. A successful read owns exactly the bytes returned.
func (b *Buffer) ReadAll(r io.Reader, size, limit int) error {
	b.Bytes = b.Bytes[:0]
	b.Resize(min(size+1, limit+1))
	used := 0
	for {
		n, err := io.ReadFull(r, b.Bytes[used:])
		used += n
		if used > limit {
			b.Bytes = b.Bytes[:used]
			return ErrLimit
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			b.Bytes = b.Bytes[:used]
			return nil
		}
		if err != nil {
			b.Bytes = b.Bytes[:used]
			return err
		}
		b.Resize(min(limit+1, max(512, used*2)))
	}
}
