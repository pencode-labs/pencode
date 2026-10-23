package main

import (
	"strings"
	"testing"

	"github.com/pencode-labs/pencode/peb"
)

func TestIconRules(t *testing.T) {
	rules, err := iconRules("0\x1f\x1ffile\n2\x1f.go\x1fgo\n1\x1fmain.go\x1fgo")
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 3 || rules[0].Match != peb.DefaultFile || rules[0].Pattern != "" ||
		rules[1].Match != peb.FileSuffix || rules[1].Pattern != ".go" ||
		rules[2].Match != peb.FileName || rules[2].Pattern != "main.go" ||
		rules[1].Path != "resources/icons/go.svg" || rules[2].Path != rules[1].Path {
		t.Fatalf("unexpected rules: %+v", rules)
	}
}

func TestIconRulesRejectMalformedTables(t *testing.T) {
	for _, data := range []string{
		"", "\n", "5\x1f.go\x1fgo", "x\x1f.go\x1fgo", "2|.go|go",
		"2\x1f.go", "2\x1f.go\x1f", "2\x1f.go\x1fgo\x1fextra",
		strings.Repeat("0\x1f\x1ffile\n", peb.MaxItems+1),
	} {
		if _, err := iconRules(data); err == nil {
			t.Fatalf("accepted malformed table with %d bytes", len(data))
		}
	}
}
