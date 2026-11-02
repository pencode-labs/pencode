package workbench

import (
	"strings"
	"testing"
)

func TestSourceDiffText(t *testing.T) {
	lines := []struct{ text, class string }{
		{"Submodule · child\r\n", ""},
		{"\r\n", ""},
		{"diff --git a/child b/child\r\n", "sc-diff-file"},
		{"index abc..def 160000\r\n", "sc-diff-meta"},
		{"--- a/child\r\n", "sc-diff-file"},
		{"+++ b/child\r\n", "sc-diff-file"},
		{"@@ -1 +1 @@\r\n", "sc-diff-hunk"},
		{"-Subproject commit abc\r\n", "sc-diff-deleted"},
		{"+Subproject commit def-dirty\r\n", "sc-diff-added"},
		{"--- removed content\r\n", "sc-diff-deleted"},
		{"+++ added content\r\n", "sc-diff-added"},
		{" unchanged <tag>&\r\n", ""},
		{"\\ No newline at end of file\r\n", "sc-diff-meta"},
		{"diff --git a/other b/other\r\n", "sc-diff-file"},
		{"--- a/other\r\n", "sc-diff-file"},
		{"+++ b/other", "sc-diff-file"},
	}
	var text strings.Builder
	for _, line := range lines {
		text.WriteString(line.text)
	}
	var b sourceVNodeView
	b.reset()
	sourceDiffText(&b, text.String())
	nodes := b.nodes()
	if len(nodes) != len(lines) {
		t.Fatalf("got %d lines, want %d", len(nodes), len(lines))
	}
	for i, node := range nodes {
		if node.Tag != "span" || node.Text != lines[i].text || len(node.Attributes) != 1 || node.Attributes[0].Value != lines[i].class {
			t.Fatalf("incorrect diff token on line %d: %+v", i, node)
		}
	}
}

func TestSourceDiffTextWarmRender(t *testing.T) {
	text := "@@ -1,200 +1,200 @@\n" + strings.Repeat("-same\n+same\n", 200)
	var b sourceVNodeView
	render := func() {
		b.reset()
		sourceDiffText(&b, text)
		b.nodes()
	}
	render()
	keys := make(map[uint64]bool)
	for _, node := range b.nodes() {
		if keys[node.Key] {
			t.Fatal("repeated diff lines share a node key")
		}
		keys[node.Key] = true
	}
	if n := testing.AllocsPerRun(100, render); n != 0 {
		t.Fatalf("warm diff text render allocated %g times", n)
	}
}
