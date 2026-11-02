package server

import (
	"context"
	"slices"
	"testing"

	"github.com/diiyw/onix/syntax"
)

func TestSyntaxGrammarEvictionKeepsActiveDocumentValid(t *testing.T) {
	services := newServices(context.Background(), Options{})
	defer services.close()
	var document syntax.Document
	defer document.Close()
	const source = "package main\nvar value = 1\n"
	if err := document.Update("live.go", source); err != nil {
		t.Fatal(err)
	}
	captures := slices.Clone(document.Captures())
	for _, name := range []string{"sample.css", "sample.html", "sample.json", "sample.md", "sample.py", "sample.c", "Sample.java"} {
		var other syntax.Document
		if err := other.Update(name, "value = 1\n"); err != nil {
			t.Fatal(err)
		}
		other.Close()
	}
	syntax.ReleaseGrammarCache()
	if err := document.Update("live.go", "package main\nvar value = 2\n"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(captures, document.Captures()) {
		t.Fatal("grammar eviction changed active document captures")
	}
	theme, err := syntax.BuiltinTheme("monokai")
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Tokens(theme, nil)) == 0 {
		t.Fatal("active document lost highlighting after eviction")
	}
}
