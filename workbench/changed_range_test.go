package workbench

import (
	"math/rand/v2"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPencodeChangedRangeUnicodeAndInvalidBytes(t *testing.T) {
	pairs := [][2]string{{"", ""}, {"abc", ""}, {"", "中文🙂"}, {"é", "ê"}, {"é", "©"}, {"first\r\n中文🙂\nlast", "first\r\n中🙂\nlast"}, {"\x80", "\x81"}, {"a\x80z", "a\x81z"}}
	random := rand.New(rand.NewPCG(1, 2))
	chars := []rune("abcé中🙂\n\r\t")
	for range 2000 {
		var left, right strings.Builder
		for range random.IntN(200) {
			left.WriteRune(chars[random.IntN(len(chars))])
		}
		original := []rune(left.String())
		start := random.IntN(len(original) + 1)
		end := start + random.IntN(len(original)-start+1)
		right.WriteString(string(original[:start]))
		for range random.IntN(8) {
			right.WriteRune(chars[random.IntN(len(chars))])
		}
		right.WriteString(string(original[end:]))
		pairs = append(pairs, [2]string{left.String(), right.String()})
	}
	for _, pair := range pairs {
		old, value := pair[0], pair[1]
		start, oldEnd, newEnd, runes := pencodeChangedRange(old, value)
		if start < 0 || oldEnd < start || oldEnd > len(old) || newEnd < start || newEnd > len(value) {
			t.Fatalf("invalid bounds for %q => %q", old, value)
		}
		if old[:start]+value[start:newEnd]+old[oldEnd:] != value || runes != utf8.RuneCountInString(old[:start]) {
			t.Fatalf("incorrect range for %q => %q", old, value)
		}
		if utf8.ValidString(old) && utf8.ValidString(value) && (!utf8.ValidString(old[:start]) || !utf8.ValidString(old[oldEnd:]) || !utf8.ValidString(value[:newEnd])) {
			t.Fatal("split UTF-8 rune")
		}
	}
}

func TestSourceHistoryRetainsDeltasAndBranches(t *testing.T) {
	base := strings.Repeat("中文🙂\r\n", 10000)
	tab := sourceTab{Kind: "changes", Result: base}
	for range 100 {
		tab.editResult(tab.Result + "x")
	}
	if tab.historyBytes != 100 || len(tab.undo) != 100 {
		t.Fatalf("history stores file snapshots: bytes=%d edits=%d", tab.historyBytes, len(tab.undo))
	}
	for range 100 {
		if !tab.undoResult(false) {
			t.Fatal("undo")
		}
	}
	if tab.Result != base {
		t.Fatal("undo changed original")
	}
	for range 100 {
		if !tab.undoResult(true) {
			t.Fatal("redo")
		}
	}
	tab.undoResult(false)
	tab.editResult(tab.Result + "中文")
	if tab.undoResult(true) || len(tab.redo) != 0 {
		t.Fatal("retained abandoned redo")
	}
	tab.editResult(strings.Repeat("z", pencodeHistoryByteLimit+1))
	if len(tab.undo) != 0 || tab.historyBytes != 0 {
		t.Fatal("oversized edit left a history gap")
	}
}
