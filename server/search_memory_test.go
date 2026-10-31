package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pencode-labs/pencode/protocol"
)

func BenchmarkSearchTextFiles(b *testing.B) {
	root := b.TempDir()
	data := []byte(strings.Repeat("abc def ghi jkl\n", (64<<10)/16))
	for i := range 100 {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("%03d.txt", i)), data, 0600); err != nil {
			b.Fatal(err)
		}
	}
	for _, test := range []struct {
		name, query string
		flags       uint32
	}{{"literal", "missing", 1}, {"fold", "missing", 0}, {"regexp", "missing[0-9]+", 4}} {
		b.Run(test.name, func(b *testing.B) {
			var request, result protocol.Encoder
			request.String(".")
			request.String(test.query)
			request.U32(test.flags)
			request.U32(0)
			var s services
			b.ReportAllocs()
			for b.Loop() {
				result.Reset()
				d := protocol.Decoder{Buffer: request.Buffer}
				if err := s.searchText(context.Background(), root, &d, &result); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
