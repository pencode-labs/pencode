package textsearch

import (
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
