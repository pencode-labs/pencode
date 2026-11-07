package terminal

import (
	"testing"
	"unsafe"
)

func TestCompactHistoryPreservesCellsAndResize(t *testing.T) {
	s := NewScreen(6, 2)
	s.HistoryLimit = 3
	line := []byte("\x1b[1;4;7;38;2;1;2;3;48;2;4;5;6me\u0301中\x1b[0m\r\n")
	s.Feed(line)
	var want [6]Cell
	copy(want[:], s.Cells[:6])
	for range 8 {
		s.Feed(line)
	}
	for row := range 3 {
		for col, got := range s.HistoryRow(row) {
			if got != want[col] {
				t.Fatalf("row=%d col=%d: got %+v want %+v", row, col, got, want[col])
			}
		}
	}
	s.Resize(8, 2)
	for row := range 3 {
		for col, got := range s.HistoryRow(row)[:6] {
			if got != want[col] {
				t.Fatal("resize changed a styled glyph or combining mark")
			}
		}
	}
	s.Resize(4, 2)
	if got := s.HistoryRow(0)[0]; got.Combining != want[0].Combining || got.Foreground != want[0].Foreground {
		t.Fatal("shrinking lost combining marks or true color")
	}
	for range 8 {
		s.Feed([]byte("\x1b[0mabc\r\n"))
	}
	if len(s.historyCombining) != 0 {
		t.Fatal("overwritten rows retained combining marks")
	}
	if allocs := testing.AllocsPerRun(100, func() { s.Feed([]byte("abc\r\n")); s.HistoryRow(0) }); allocs != 0 {
		t.Fatalf("warm scroll/read allocated %g", allocs)
	}
}

func TestCompactHistoryMemoryBudget(t *testing.T) {
	if unsafe.Sizeof(historyCell{}) != 16 {
		t.Fatal("unexpected compact cell size")
	}
	for _, cols := range []int{120, 200} {
		s := NewScreen(cols, 24)
		for range 2200 {
			s.Feed([]byte("line\r\n"))
		}
		bytes := cap(s.History) * int(unsafe.Sizeof(historyCell{}))
		if bytes > cols*s.HistoryLimit*18 {
			t.Fatalf("history capacity exceeds compact budget: %d", bytes)
		}
		t.Logf("cols=%d rows=%d history=%.3f MiB", cols, len(s.History)/cols, float64(bytes)/(1<<20))
	}
}
