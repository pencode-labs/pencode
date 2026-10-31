package textsearch

import (
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestSearchAndReplace(t *testing.T) {
	for _, tc := range []struct {
		name, content, query, replacement, want string
		options                                 Options
		count                                   int
	}{
		{"fold", "Go go GO", "go", "x", "x x x", Options{}, 3},
		{"case", "Go go GO", "go", "x", "Go x GO", Options{CaseSensitive: true}, 1},
		{"unicode word", "你 你好 你_ 你\n", "你", "我", "我 你好 你_ 我\n", Options{WholeWord: true}, 2},
		{"literal dollar", "a.b", ".", "$1", "a$1b", Options{}, 1},
		{"captures", "ab12 ab34", `(?P<name>ab)(\d+)`, "${name}-$2", "ab-12 ab-34", Options{Regex: true}, 2},
		{"whole match", "a1", `a\d`, "$&:$$", "a1:$", Options{Regex: true}, 1},
		{"capture suffix", "a1", `a(\d)`, "$1suffix", "1suffix", Options{Regex: true}, 1},
		{"escaped dollar", "a1", `a\d`, "$$&", "$&", Options{Regex: true}, 1},
		{"replacement escapes", "a1", `a\d`, `\n\t`, "\n\t", Options{Regex: true}, 1},
		{"multiline", "a\r\nb\r\n", `^`, ">", ">a\r\n>b\r\n>", Options{Regex: true}, 3},
		{"zero width unicode", "中🙂", `(?:)`, "|", "|中|🙂|", Options{Regex: true}, 3},
		{"inserted not revisited", "aaa", "a", "aa", "aaaaaa", Options{}, 3},
		{"preserve case", "foo FOO Foo fOo", "foo", "bar", "bar BAR Bar bar", Options{PreserveCase: true}, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := Compile(tc.query, tc.options)
			if err != nil {
				t.Fatal(err)
			}
			matches := p.FindAll(tc.content, nil, -1)
			if len(matches) != tc.count {
				t.Fatalf("matches=%v", matches)
			}
			if got := p.Replace(tc.content, tc.replacement, matches); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func checkRegexpRange(t testing.TB, content, query string, wholeWord bool, start, end, limit int, countAll bool) {
	t.Helper()
	p, err := Compile(query, Options{Regex: true, WholeWord: wholeWord})
	if err != nil || query == "" {
		return
	}
	var want []Match
	count := 0
	for _, groups := range p.re.FindAllStringSubmatchIndex(content, -1) {
		if groups[0] < start || groups[1] > end || !p.boundary(content, groups[0], groups[1]) {
			continue
		}
		count++
		if limit < 0 || len(want) < limit {
			want = append(want, Match{groups[0], groups[1], groups})
		}
		if !countAll && limit > 0 && count == limit {
			break
		}
	}
	if limit == 0 && !countAll {
		count = 0
	}
	got, total := p.FindRange(content, nil, start, end, limit, countAll)
	byteMatches, byteTotal := p.FindRange([]byte(content), nil, start, end, limit, countAll)
	if byteTotal != total || !reflect.DeepEqual(byteMatches, got) {
		t.Fatalf("byte and string search differ for %q / %q: %v/%d != %v/%d", content, query, byteMatches, byteTotal, got, total)
	}
	if total != count || !slices.EqualFunc(got, want, func(a, b Match) bool {
		const replacement = `$0|$1|${word}`
		groups := a.groups
		if groups == nil {
			groups = []int{a.Start, a.End}
		}
		return a.Start == b.Start && a.End == b.End && slices.Equal(groups, b.groups) && p.Replacement(content, a, replacement) == string(p.re.ExpandString(nil, replacement, content, b.groups))
	}) {
		t.Fatalf("%q / %q, whole=%t range=%d:%d limit=%d countAll=%t: got %v/%d want %v/%d", content, query, wholeWord, start, end, limit, countAll, got, total, want, count)
	}
}

func TestWarmByteSearchDoesNotAllocate(t *testing.T) {
	content := []byte(strings.Repeat("K k K S s ſ Σ σ ς\n", 1000))
	for _, options := range []Options{{CaseSensitive: true}, {}, {WholeWord: true}, {Regex: true}} {
		p, err := Compile("k", options)
		if err != nil {
			t.Fatal(err)
		}
		dst, want := p.FindRange(content, nil, 0, len(content), 20, true)
		if n := testing.AllocsPerRun(20, func() {
			var count int
			dst, count = p.FindRange(content, dst, 0, len(content), 20, true)
			if count != want || len(dst) != 20 {
				panic("byte search lost matches")
			}
		}); n != 0 {
			t.Fatalf("options=%+v: byte search allocated %g times", options, n)
		}
	}
}

func TestRegexpIterationPreservesContextAndCaptures(t *testing.T) {
	for _, content := range []string{"", "aaa\na a\n", "a\r\nb\r\n", "a界🙂 a\xffb", "ab ax ab", " b b "} {
		for _, query := range []string{`a`, `(?i)a`, `a\.`, `\x{FFFD}`, `^`, `$`, `\A|a`, `\z`, `\b`, `\B`, `a*`, `a*?`, `(?:)`, `a|aa`, `a($)|(a)`, `(^a)|(\ba\b)|(a)`, `(?m)^.*$`, `(?-m)^.*$`, `(?s)(.*)`, `(?P<a>a)?(b)`, `\b(a+)|\B(b)`, `\A(?:a*)|(?m:^b)`} {
			for _, wholeWord := range []bool{false, true} {
				for _, countAll := range []bool{false, true} {
					for _, limit := range []int{-1, 0, 1, 3} {
						for _, bounds := range [][2]int{{0, len(content)}, {min(1, len(content)), len(content)}, {0, len(content) / 2}} {
							checkRegexpRange(t, content, query, wholeWord, bounds[0], bounds[1], limit, countAll)
						}
					}
				}
			}
		}
	}
}

func TestLiteralRegexpCountDoesNotAllocate(t *testing.T) {
	content := strings.Repeat("K k K ", 10000)
	p, err := Compile(`(?i)k`, Options{Regex: true})
	if err != nil {
		t.Fatal(err)
	}
	dst, _ := p.FindRange(content, nil, 0, len(content), 5000, true)
	if n := testing.AllocsPerRun(20, func() {
		var count int
		dst, count = p.FindRange(content, dst, 0, len(content), 5000, true)
		if count != 30000 || len(dst) != 5000 {
			t.Fatal(len(dst), count)
		}
	}); n != 0 {
		t.Fatalf("warm literal regexp count allocated %g times", n)
	}
	if got := p.Replacement(content, dst[0], `[$0]`); got != "[K]" {
		t.Fatal("literal regexp lost replacement captures", got)
	}
}

func FuzzRegexpIteration(f *testing.F) {
	for _, query := range []string{`a*`, `\b(a)|(b)`, `\A|^|$|\z`, `(?s:.*)`, `(a$)|(a)`, `\B`, `(?:)`, `(?P<word>\w+)`} {
		f.Add("a\r\nb界🙂 a\n", query, false, uint8(1), uint8(9), int8(2), true)
	}
	f.Add("\xffa\xfe", `\x{FFFD}`, false, uint8(0), uint8(3), int8(2), true)
	f.Fuzz(func(t *testing.T, content, query string, wholeWord bool, first, last uint8, limit int8, countAll bool) {
		if len(content) > 4096 || len(query) > 256 {
			t.Skip()
		}
		start, end := int(first)%(len(content)+1), int(last)%(len(content)+1)
		if start > end {
			start, end = end, start
		}
		checkRegexpRange(t, content, query, wholeWord, start, end, int(limit)%8, countAll)
	})
}

func BenchmarkRegexpCountBounded(b *testing.B) {
	content := strings.Repeat("a ", 1<<19)
	for _, query := range []string{`a`, `(a)`, `\ba\b`} {
		b.Run(query, func(b *testing.B) {
			p, _ := Compile(query, Options{Regex: true})
			dst, _ := p.FindRange(content, nil, 0, len(content), 5000, true)
			b.ReportAllocs()
			for b.Loop() {
				var count int
				dst, count = p.FindRange(content, dst, 0, len(content), 5000, true)
				if len(dst) != 5000 || count != 1<<19 {
					b.Fatal(len(dst), count)
				}
			}
		})
	}
}

func TestInvalidAndLimitedSearch(t *testing.T) {
	p, err := Compile("[", Options{Regex: true})
	if err == nil || p != nil || len(p.FindAll("[", nil, -1)) != 0 {
		t.Fatal("invalid regex became a literal query")
	}
	p, _ = Compile("", Options{})
	if len(p.FindAll("abc", nil, -1)) != 0 {
		t.Fatal("empty input searched everything")
	}
	p, _ = Compile("a", Options{CaseSensitive: true})
	matches := p.FindAll(strings.Repeat("a", 100), nil, 3)
	if len(matches) != 3 {
		t.Fatal(len(matches))
	}
	if allocations := testing.AllocsPerRun(100, func() { matches = p.FindAll("aaa", matches, 3) }); allocations != 0 {
		t.Fatal("literal search allocated", allocations)
	}
}

func TestLiteralFoldMatchesRegexp(t *testing.T) {
	for _, text := range []string{"K k K S s ſ Σ σ ς", "你 你好 你_ 你", "aaa", "Go go GO", "中😀中😀", "İ i I ı"} {
		for _, query := range []string{"k", "S", "σ", "你", "aa", "go", "😀", "i"} {
			p, _ := Compile(query, Options{})
			got := p.FindAll(text, nil, -1)
			want := regexp.MustCompile("(?i)"+regexp.QuoteMeta(query)).FindAllStringIndex(text, -1)
			if len(got) != len(want) {
				t.Fatalf("%q/%q: %v != %v", text, query, got, want)
			}
			for i, m := range got {
				if m.Start != want[i][0] || m.End != want[i][1] {
					t.Fatal(text, query, got, want)
				}
			}
		}
	}
}

func TestBoundedSearchCountsAndScopes(t *testing.T) {
	text := strings.Repeat("a ", 1<<19)
	for _, options := range []Options{{}, {WholeWord: true}, {CaseSensitive: true}} {
		p, _ := Compile("a", options)
		dst, count := p.FindRange(text, nil, 0, len(text), 3, true)
		if len(dst) != 3 || count != 1<<19 {
			t.Fatal(len(dst), count)
		}
		if n := testing.AllocsPerRun(10, func() { dst = p.FindAll(text, dst, 3) }); n != 0 {
			t.Fatal("warm literal allocation", n)
		}
	}
	for _, options := range []Options{{}, {Regex: true}, {Regex: true, WholeWord: true}} {
		p, _ := Compile("aa", options)
		all := p.FindAll("aaa aa", nil, -1)
		var want []Match
		for _, m := range all {
			if m.Start >= 1 && m.End <= 6 {
				want = append(want, m)
			}
		}
		got, _ := p.FindRange("aaa aa", nil, 1, 6, 3, false)
		if !reflect.DeepEqual(got, want) {
			t.Fatal(options, got, want)
		}
	}
	// Filtering must not reinterpret ^, $, empty matches or capture positions.
	for _, query := range []string{`^`, `$`, `\b`, `(a*)`, `(?:)`, `a|aa`} {
		p, _ := Compile(query, Options{Regex: true, WholeWord: true})
		text := "aaa\na a\n"
		all := p.FindAll(text, nil, -1)
		for limit := 1; limit < 5; limit++ {
			got := p.FindAll(text, nil, limit)
			if !reflect.DeepEqual(got, all[:min(limit, len(all))]) {
				t.Fatal(query, got, all)
			}
		}
	}
}

func BenchmarkSearchDense(b *testing.B) {
	content := strings.Repeat("a ", 1<<19)
	for _, tc := range []struct {
		name    string
		options Options
	}{{"literal", Options{CaseSensitive: true}}, {"fold", Options{}}, {"whole_word", Options{WholeWord: true}}} {
		b.Run(tc.name, func(b *testing.B) {
			p, _ := Compile("a", tc.options)
			dst := p.FindAll(content, nil, 5000)
			b.ReportAllocs()
			for b.Loop() {
				dst = p.FindAll(content, dst, 5000)
			}
		})
	}
}
