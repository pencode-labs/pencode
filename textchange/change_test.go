package textchange

import (
	"math/rand/v2"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBetweenReconstructsEdits(t *testing.T) {
	random := rand.New(rand.NewPCG(31, 9))
	parts := []string{"a", "中", "😀", "\r", "\n", "\r\n", ""}
	for range 3000 {
		var a, b strings.Builder
		common := strings.Repeat("abc中😀\r\n", random.IntN(100))
		a.WriteString(common)
		b.WriteString(common)
		for range random.IntN(20) {
			a.WriteString(parts[random.IntN(len(parts))])
		}
		for range random.IntN(20) {
			b.WriteString(parts[random.IntN(len(parts))])
		}
		a.WriteString(common)
		b.WriteString(common)
		old, next := a.String(), b.String()
		start, end, newEnd := Between(old, next)
		if old[:start]+next[start:newEnd]+old[end:] != next || !utf8.ValidString(next[start:newEnd]) {
			t.Fatal(old, next, start, end, newEnd)
		}
		for _, edge := range []struct {
			s string
			i int
		}{{old, start}, {old, end}, {next, start}, {next, newEnd}} {
			if edge.i > 0 && edge.i < len(edge.s) && edge.s[edge.i-1] == '\r' && edge.s[edge.i] == '\n' {
				t.Fatal("split CRLF", edge)
			}
		}
	}
}
