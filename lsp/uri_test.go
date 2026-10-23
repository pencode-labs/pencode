package lsp

import "testing"

func TestServerFileURIsAreIndependentOfGUIPlatform(t *testing.T) {
	for _, name := range []string{"/home/user/a b/中文.go", "C:/Users/name/a b.go"} {
		uri := FileURI(name)
		got, err := uri.Path()
		if err != nil || got != name {
			t.Fatalf("%s -> %s -> %s: %v", name, uri, got, err)
		}
	}
	if FileURI(`C:\Users\name\main.go`) != FileURI("C:/Users/name/main.go") {
		t.Fatal("server drive paths depend on GUI path separators")
	}
}
