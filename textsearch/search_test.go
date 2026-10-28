package textsearch

import (
	"reflect"
	"regexp"
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
