package server

import (
	"context"
	"testing"

	"github.com/diiyw/onix/syntax"
	"github.com/odvcencio/gotreesitter/grammars"
	grammarruntime "github.com/odvcencio/gotreesitter/grammars/runtime"
)

func TestPencodeGrammarBudgetKeepsActiveDocumentValid(t *testing.T) {
	_, previousLimit := grammarruntime.EmbeddedLanguageCacheStats()
	t.Cleanup(func() {
		grammarruntime.SetEmbeddedLanguageCacheLimit(previousLimit)
		grammarruntime.PurgeEmbeddedLanguageCache()
	})
	grammarruntime.PurgeEmbeddedLanguageCache()
	services := newServices(context.Background(), Options{})
	defer services.close()
	var document syntax.Document
	defer document.Close()
	if err := document.Update("live.go", "package main\nvar value = 1\n"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"sample.css", "sample.html", "sample.json", "sample.md", "sample.py", "sample.c", "Sample.java"} {
		entry := grammars.DetectLanguage(name)
		if entry == nil || entry.Language() == nil {
			t.Fatalf("missing grammar: %s", name)
		}
		loaded, limit := grammarruntime.EmbeddedLanguageCacheStats()
		if limit != 4 || loaded > limit {
			t.Fatalf("decoded grammars=%d limit=%d", loaded, limit)
		}
	}
	if err := document.Update("live.go", "package main\nvar value = 2\n"); err != nil {
		t.Fatal(err)
	}
	if document.Tree() == nil || document.Tree().RootNode() == nil {
		t.Fatal("eviction invalidated the active document")
	}
	theme, err := syntax.BuiltinTheme("monokai")
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Tokens(theme, nil)) == 0 {
		t.Fatal("active document lost highlighting after eviction")
	}
}
