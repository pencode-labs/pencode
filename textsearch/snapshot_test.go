package textsearch

import (
	"reflect"
	"strings"
	"testing"

	"github.com/onix-engine/onix/text/piecetree"
)

func TestSnapshotSearchMatchesFlatText(t *testing.T) {
	base := strings.Repeat("prefix line\n", 400) + "foo Foo food\n中🙂 K K S ſ\r\nfoo\ninvalid \xff\n"
	offset := strings.Index(base, "Foo")
	snapshot := piecetree.FromString(base).Replace(offset, offset+3, "foo")
	flat := base[:offset] + "foo" + base[offset+3:]
	for _, query := range []string{"foo", "oo\nf", "K", "S", "中🙂", "^foo", `\bfoo\b`, `(f)(o+)`, `^|$`, `\Afoo|foo\z`, `[^\n]+`, "�"} {
		for _, options := range []Options{{}, {CaseSensitive: true}, {WholeWord: true}, {Regex: true}, {Regex: true, WholeWord: true, CaseSensitive: true}} {
			p, err := Compile(query, options)
			if err != nil {
				t.Fatal(err)
			}
			for _, start := range []int{0, offset, offset + 1} {
				for _, limit := range []int{-1, 1, 4} {
					want, _ := p.FindRange(flat, nil, start, len(flat)-1, limit, false)
					got := p.FindSnapshot(snapshot, nil, start, len(flat)-1, limit)
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("%q %+v start=%d limit=%d: got %+v want %+v", query, options, start, limit, got, want)
					}
				}
			}
		}
	}
	if snapshot.Materialized() {
		t.Fatal("search materialized its input")
	}
}

func TestSnapshotLiteralSearchWarmAllocations(t *testing.T) {
	source := piecetree.FromString("hello 中🙂 word\nWORD words").Replace(6, 9, "文")
	for _, options := range []Options{{CaseSensitive: true}, {WholeWord: true}} {
		p, _ := Compile("word", options)
		dst := p.FindSnapshot(source, nil, 0, source.Len(), -1)
		if allocs := testing.AllocsPerRun(100, func() { dst = p.FindSnapshot(source, dst, 0, source.Len(), -1) }); allocs != 0 {
			t.Fatalf("warm literal search allocated %g", allocs)
		}
	}
}
