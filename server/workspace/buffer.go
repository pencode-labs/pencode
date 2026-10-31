package workspace

import (
	"crypto/sha256"
	"hash"
	"io"
	"strings"
	"sync"

	"github.com/pencode-labs/pencode/protocol"
)

type fileBuffer struct {
	data [protocol.ChunkSize]byte
	hash hash.Hash
	sum  [sha256.Size]byte
}

var fileBuffers = sync.Pool{New: func() any { return &fileBuffer{hash: sha256.New()} }}

func (b *fileBuffer) read(r io.Reader, text *strings.Builder) ([sha256.Size]byte, error) {
	b.hash.Reset()
	total := 0
	for {
		n, err := r.Read(b.data[:])
		total += n
		if total > protocol.MaxMessage-1024 {
			return b.sum, failure(protocol.Invalid, "文件超过文档预算")
		}
		if text != nil {
			text.Write(b.data[:n])
		}
		b.hash.Write(b.data[:n])
		if err == io.EOF {
			b.hash.Sum(b.sum[:0])
			return b.sum, nil
		}
		if err != nil {
			return b.sum, err
		}
	}
}
