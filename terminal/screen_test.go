package terminal

import (
	"strings"
	"testing"
)

func screenLine(s *Screen, row int) string {
	var b strings.Builder
	for _, c := range s.Cells[row*s.Cols : (row+1)*s.Cols] {
		if c.Width != 0 {
			b.WriteRune(c.Rune)
			for _, r := range c.Combining {
				if r != 0 {
					b.WriteRune(r)
				}
			}
		}
	}
	return b.String()
}
func TestScreenStreamingUnicodeAndControl(t *testing.T) {
	s := NewScreen(12, 3)
	for _, b := range []byte("hello\rXY\x1b[2;1H中e\u0301!") {
		s.Feed([]byte{b})
	}
	if got := screenLine(s, 0); got != "XYllo       " {
		t.Fatal(got)
	}
	if got := screenLine(s, 1); got != "中é!        " {
		t.Fatal(got)
	}
	if x, y := s.Cursor(); x != 4 || y != 1 {
		t.Fatalf("cursor %d %d", x, y)
	}
	s.Feed([]byte("\x1b[2K"))
	if got := screenLine(s, 1); got != strings.Repeat(" ", 12) {
		t.Fatal(got)
	}
}
func TestScreenColorsAlternateAndReplies(t *testing.T) {
	s := NewScreen(8, 3)
	s.Feed([]byte("main\x1b[31;44mR\x1b[38;2;1;2;3mT"))
	if s.Cells[4].Foreground != Palette[1] || s.Cells[4].Background != Palette[4] || s.Cells[5].Foreground != 0x010203 {
		t.Fatal(s.Cells[:6])
	}
	s.Feed([]byte("\x1b[?1049h\x1b[Hfull\x1b[?25l\x1b[?1h\x1b[?2004h"))
	if !s.Alternate() || s.CursorVisible || !s.ApplicationCursor || !s.BracketedPaste {
		t.Fatal("modes")
	}
	s.Feed([]byte("\x1b[?1049l\x1b[6n\x1b[5n\x1b[c"))
	if got := screenLine(s, 0); got != "mainRT  " {
		t.Fatal(got)
	}
	if got := string(s.Replies()); got != "\x1b[1;7R\x1b[0n\x1b[?1;2c" {
		t.Fatalf("reply %q", got)
	}
	if len(s.Replies()) != 0 {
		t.Fatal("reply retained")
	}
}
func TestScreenScrollRegionsHistoryAndResize(t *testing.T) {
	s := NewScreen(5, 3)
	s.HistoryLimit = 2
	s.Feed([]byte("11111\r\n22222\r\n33333\r\n44444"))
	if len(s.History) != 5 || screenLine(s, 0) != "22222" {
		t.Fatalf("scroll %q %d", screenLine(s, 0), len(s.History))
	}
	s.Feed([]byte("\x1b[2;3r\x1b[3;1H\n"))
	if screenLine(s, 0) != "22222" || screenLine(s, 1) != "44444" || len(s.History) != 5 {
		t.Fatal("scroll region")
	}
	s.Resize(7, 4)
	if screenLine(s, 0) != "22222  " || len(s.History) != 7 {
		t.Fatal("resize")
	}
	s.Feed([]byte("\x1b[?1049h"))
	s.Resize(3, 2)
	s.Feed([]byte("\x1b[?1049l"))
	if screenLine(s, 0) != "222" {
		t.Fatal("alternate resize")
	}
}
func TestScreenOSCAndOverflow(t *testing.T) {
	s := NewScreen(10, 2)
	s.Feed([]byte("\x1b]2;hello\x1b\\ok"))
	if s.Title != "hello" || !strings.HasPrefix(screenLine(s, 0), "ok") {
		t.Fatal("OSC")
	}
	s.Feed([]byte("\x1b]10;?\a\x1b]11;?\x1b\\\x1b[>c"))
	if got := string(s.Replies()); got != "\x1b]10;rgb:e5e5/e5e5/e5e5\x1b\\\x1b]11;rgb:1e1e/1e1e/1e1e\x1b\\\x1b[>0;1;0c" {
		t.Fatalf("color/device replies %q", got)
	}

	s.Feed([]byte("\x1b]52;" + strings.Repeat("x", 10000) + "\a!"))
	if !strings.HasPrefix(screenLine(s, 0), "ok!") {
		t.Fatal("OSC overflow leaked")
	}
	s.Feed([]byte("\x1b[" + strings.Repeat("1;", 10000) + "mZ"))
	if !strings.HasPrefix(screenLine(s, 0), "ok!Z") {
		t.Fatal("CSI overflow leaked")
	}
}
func TestScreenFeedSteadyStateAllocations(t *testing.T) {
	s := NewScreen(80, 24)
	p := []byte("\x1b[H\x1b[31mhello 世界\x1b[0m\x1b[K")
	s.Feed(p)
	if n := testing.AllocsPerRun(100, func() { s.Feed(p) }); n != 0 {
		t.Fatalf("feed allocated %g", n)
	}
}
func TestScreenWrapSurvivesSGR(t *testing.T) {
	s := NewScreen(3, 2)
	s.Feed([]byte("abc\x1b[31md"))
	if screenLine(s, 0) != "abc" || screenLine(s, 1) != "d  " {
		t.Fatal("SGR cancelled pending wrap")
	}
}

func TestScreenHistoryRingAndGraphics(t *testing.T) {
	s := NewScreen(4, 2)
	s.HistoryLimit = 3
	for _, line := range []string{"one", "two", "tri", "four", "five", "six"} {
		s.Feed([]byte(line + "\r\n"))
	}
	if len(s.History) != 12 || s.Scrolled != 5 {
		t.Fatal("scrollback was not bounded")
	}
	for i, want := range []string{"tri ", "four", "five"} {
		var b strings.Builder
		for _, c := range s.HistoryRow(i) {
			b.WriteRune(c.Rune)
		}
		if b.String() != want {
			t.Fatalf("history %d: %q want %q", i, b.String(), want)
		}
	}
	s.Resize(6, 2)
	if len(s.History) != 18 || s.HistoryRow(0)[0].Rune != 't' {
		t.Fatal("resize lost history")
	}
	s.Feed([]byte("\x1b[H\x1b(0lqk\x1b(B\x1b[?1002h\x1b[?1006h"))
	if screenLine(s, 0) != "┌─┐   " || s.MouseTracking != 1002 || !s.MouseSGR {
		t.Fatal("graphics or mouse modes")
	}
	data := []byte("\x1b[H\x1b(0lqk\x1b(B")
	if n := testing.AllocsPerRun(100, func() { s.Feed(data) }); n != 0 {
		t.Fatalf("graphics parser allocated %g", n)
	}
	s.Feed([]byte("\x1b[?1002l\x1b[?1006l"))
	if s.MouseTracking != 0 || s.MouseSGR {
		t.Fatal("mouse mode reset")
	}
}

func TestScreenClearOutputPreservesInput(t *testing.T) {
	s := NewScreen(24, 4)
	for range 10 {
		s.Feed([]byte("old output\r\n"))
	}
	s.Feed([]byte("\x1b[2;1H\x1b[32mPS C:\\项目> e\u0301中\x1b[0m\x1b[2D\x1b7"))
	var line [24]Cell
	copy(line[:], s.Cells[s.Cols:2*s.Cols])
	x, y := s.Cursor()
	saved, style, revision := s.saved, s.style, s.Revision
	s.ClearOutput()
	if cx, cy := s.Cursor(); cx != x || cy != y || s.saved != saved || s.style != style {
		t.Fatal("clear changed application cursor or rendition state")
	}
	if len(s.History) != 0 || s.historyHead != 0 || s.Revision <= revision {
		t.Fatal("clear retained history or failed to invalidate the screen")
	}
	for row := range s.Rows {
		for col := range s.Cols {
			want := s.blank()
			if row == y {
				want = line[col]
			}
			if got := s.Cells[row*s.Cols+col]; got != want {
				t.Fatalf("cell %d,%d: %+v, want %+v", col, row, got, want)
			}
		}
	}
	// ConPTY can resume with absolute cursor positioning rather than a redraw.
	s.Feed([]byte("\x1b[2;24HX\x1b[6n"))
	if s.Cells[2*s.Cols-1].Rune != 'X' || s.Cells[s.Cols] != line[0] || string(s.Replies()) != "\x1b[2;24R" {
		t.Fatal("absolute output after clear lost the prompt or cursor position")
	}
}

func TestScreenClearOutputPreservesWrapAndAlternateScreen(t *testing.T) {
	s := NewScreen(4, 2)
	s.Feed([]byte("old\r\nabcd"))
	s.ClearOutput()
	s.Feed([]byte("X"))
	if screenLine(s, 0) != "abcd" || screenLine(s, 1) != "X   " {
		t.Fatal("clear cancelled pending wrap")
	}
	s.Feed([]byte("\x1b[?1049hTUI"))
	revision, history := s.Revision, len(s.History)
	s.ClearOutput()
	if screenLine(s, 0) != "TUI " || s.Revision != revision || len(s.History) != history {
		t.Fatal("UI clear changed the alternate screen or primary history")
	}
	s.Feed([]byte("\x1b[?1049l\x1bc"))
	if screenLine(s, 0) != "    " || screenLine(s, 1) != "    " || len(s.History) != 0 {
		t.Fatal("terminal reset retained the input line")
	}
}

func TestScreenClearReleasesLargeHistory(t *testing.T) {
	for _, method := range []string{"reset", "output", "escape"} {
		s := NewScreen(120, 24)
		line := []byte("line\r\n")
		for range 2200 {
			s.Feed(line)
		}
		if len(s.History)/s.Cols != s.HistoryLimit || s.historyHead == 0 {
			t.Fatal("fixture did not wrap the scrollback ring")
		}
		revision := s.Revision
		switch method {
		case "escape":
			s.Feed([]byte("\x1b[3J"))
		case "output":
			s.ClearOutput()
		default:
			s.Clear()
		}
		if len(s.History) != 0 || cap(s.History) != 0 || s.historyHead != 0 || s.Revision <= revision {
			t.Fatal("clear retained large history or failed to invalidate the screen")
		}
		s.Feed([]byte("\x1b[H"))
		for range 25 {
			s.Feed(line)
		}
		if len(s.History) == 0 || s.HistoryRow(0)[0].Rune != 'l' {
			t.Fatal("scrollback failed after releasing storage")
		}
		// Small histories stay reusable even when an application clears them often.
		if n := testing.AllocsPerRun(100, func() {
			if method == "output" {
				s.ClearOutput()
			} else {
				s.Clear()
			}
			s.Feed(line)
		}); n != 0 {
			t.Fatalf("warm clear/scroll allocated %g times", n)
		}
	}
}
