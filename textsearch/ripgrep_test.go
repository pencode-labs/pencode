package textsearch

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/onix-engine/onix/text/piecetree"
)

func TestRipgrepReadsEditedPiecesAndByteOffsets(t *testing.T) {
	base := strings.Repeat("prefix ", 1000) + "中 K K k xk\r\nfoo\nbar\x00tail" + strings.Repeat("z", localFindBytes)
	value := piecetree.FromString(base).Replace(1, 2, "xx")
	for _, q := range []string{"k", "foo\nbar", "tail", "missing", "--glob", " "} {
		for _, word := range []bool{false, true} {
			p, err := Compile(q, Options{WholeWord: word})
			if err != nil {
				t.Fatal(err)
			}
			for _, limit := range []int{1, -1} {
				want := p.FindSnapshot(value, nil, 3, value.Len(), limit)
				got, err := p.SearchSnapshot(t.Context(), value, nil, 3, value.Len(), limit)
				if err != nil || !reflect.DeepEqual(want, got) {
					t.Fatalf("%q word=%t limit=%d: %v vs %v, %v", q, word, limit, want, got, err)
				}
			}
		}
	}
	if value.Materialized() {
		t.Fatal("search flattened the edited document")
	}
}

func TestSmallSnapshotSearchReusesMatches(t *testing.T) {
	s := piecetree.FromString("one k K xk end").Replace(6, 7, "K")
	p, err := Compile("K", Options{WholeWord: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	var dst []Match
	check := func() {
		dst, err = p.SearchSnapshot(ctx, s, dst, 4, s.Len(), 3)
		if err != nil || len(dst) != 2 || dst[0].Start != 4 || dst[1].End != 9 {
			t.Fatal(dst, err)
		}
	}
	check()
	if allocs := testing.AllocsPerRun(100, check); allocs != 0 || s.Materialized() {
		t.Fatalf("small snapshot search allocated %g times, materialized=%t", allocs, s.Materialized())
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := p.SearchSnapshot(ctx, s, dst, 0, s.Len(), -1); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestRegexpDiskFilterRequiresLiteral(t *testing.T) {
	for _, tc := range []struct {
		expr, want string
		sensitive  bool
	}{
		{"missing[0-9]+", "missing", false},
		{"(ab|xy)?(?-i:Exact)[0-9]", "Exact", true},
		{"(foo){2,4}.*(longer)?", "foo", false},
		{"(foo|bar)[0-9]", "", true},
		{"(foo){0,3}[0-9]", "", true},
		{"^foo|bar$", "", true},
	} {
		p, err := Compile(tc.expr, Options{Regex: true})
		if err != nil {
			t.Fatal(err)
		}
		got, sensitive := p.DiskFilter()
		if !strings.EqualFold(got, tc.want) || sensitive != tc.sensitive || sensitive && got != tc.want {
			t.Fatalf("%q: got (%q,%t), want (%q,%t)", tc.expr, got, sensitive, tc.want, tc.sensitive)
		}
	}
}
