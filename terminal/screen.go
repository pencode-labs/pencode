// Package terminal implements a streaming VT screen and native shell sessions.
// It has no dependencies outside the Go standard library.
package terminal

import (
	"strconv"
	"unicode"
	"unicode/utf8"
)

type Color uint32

const Default Color = 1 << 24

type Cell struct {
	Rune                     rune
	Foreground, Background   Color
	Bold, Inverse, Underline bool
	Width                    uint8
	Combining                [3]rune
}
type cursor struct {
	x, y  int
	style Cell
	wrap  bool
}

// Screen is owned by the foreground. Feed accepts arbitrary byte boundaries.
type Screen struct {
	Cols, Rows                                       int
	Cells                                            []Cell
	History                                          []Cell
	HistoryLimit                                     int
	historyHead                                      int
	Scrolled                                         uint64
	Title                                            string
	DefaultForeground, DefaultBackground             Color
	CursorVisible, ApplicationCursor, BracketedPaste bool
	Revision                                         uint64
	MouseTracking                                    int
	MouseSGR                                         bool
	charsets                                         [2]bool
	activeCharset, designateCharset                  int
	x, y, top, bottom                                int
	style                                            Cell
	saved                                            cursor
	primary                                          []Cell
	primaryCursor                                    cursor
	alternate                                        bool
	autowrap, origin, insert, wrap                   bool
	state                                            uint8
	sequence                                         [4096]byte
	length                                           int
	sequenceOverflow                                 bool
	pending                                          [utf8.UTFMax]byte
	pendingLen                                       int
	params                                           [32]int
	reply                                            []byte
}

func NewScreen(cols, rows int) *Screen {
	s := &Screen{HistoryLimit: 2000, CursorVisible: true, autowrap: true, DefaultForeground: 0xe5e5e5, DefaultBackground: 0x1e1e1e}
	s.style = Cell{Foreground: Default, Background: Default}
	s.saved.style = s.style
	s.Resize(cols, rows)
	return s
}

// HistoryRow returns a chronological row from the bounded scrollback ring.
func (s *Screen) HistoryRow(index int) []Cell {
	rows := len(s.History) / s.Cols
	start := ((s.historyHead + index) % rows) * s.Cols
	return s.History[start : start+s.Cols]
}
func (s *Screen) Cursor() (int, int) { return s.x, s.y }
func (s *Screen) Alternate() bool    { return s.alternate }
func (s *Screen) Replies() []byte    { p := s.reply; s.reply = s.reply[:0]; return p }
func (s *Screen) blank() Cell {
	c := s.style
	c.Rune = ' '
	c.Width = 1
	c.Combining = [3]rune{}
	return c
}
func (s *Screen) fill(cells []Cell) {
	c := s.blank()
	for i := range cells {
		cells[i] = c
	}
}
func (s *Screen) Resize(cols, rows int) {
	cols, rows = max(2, min(cols, 1000)), max(1, min(rows, 500))
	if cols == s.Cols && rows == s.Rows {
		return
	}
	oldCols, oldRows := s.Cols, s.Rows
	resize := func(old []Cell) []Cell {
		cells := make([]Cell, cols*rows)
		s.fill(cells)
		for y := 0; y < min(rows, oldRows); y++ {
			copy(cells[y*cols:y*cols+min(cols, oldCols)], old[y*oldCols:y*oldCols+min(cols, oldCols)])
		}
		return cells
	}
	s.Cells = resize(s.Cells)
	if s.alternate {
		s.primary = resize(s.primary)
	}
	if oldCols != 0 && oldCols != cols && len(s.History) > 0 {
		count := len(s.History) / oldCols
		history := make([]Cell, count*cols)
		s.fill(history)
		for row := 0; row < count; row++ {
			copy(history[row*cols:row*cols+min(cols, oldCols)], s.HistoryRow(row)[:min(cols, oldCols)])
		}
		s.History = history
		s.historyHead = 0
	}
	s.Cols, s.Rows = cols, rows
	s.top, s.bottom = 0, rows-1
	s.x, s.y = min(s.x, cols-1), min(s.y, rows-1)
	s.wrap = false
	s.Revision++
}
func (s *Screen) Clear() { s.fill(s.Cells); s.History = s.History[:0]; s.historyHead = 0; s.Revision++ }
func (s *Screen) scroll(n int) {
	n = max(-(s.bottom - s.top + 1), min(n, s.bottom-s.top+1))
	start, end := s.top*s.Cols, (s.bottom+1)*s.Cols
	if n > 0 {
		if !s.alternate && s.top == 0 && s.bottom == s.Rows-1 && s.HistoryLimit > 0 {
			for row := 0; row < n; row++ {
				line := s.Cells[(s.top+row)*s.Cols : (s.top+row+1)*s.Cols]
				if len(s.History) < s.HistoryLimit*s.Cols {
					s.History = append(s.History, line...)
				} else {
					copy(s.History[s.historyHead*s.Cols:(s.historyHead+1)*s.Cols], line)
					s.historyHead = (s.historyHead + 1) % s.HistoryLimit
				}
				s.Scrolled++
			}
		}
		copy(s.Cells[start:end], s.Cells[start+n*s.Cols:end])
		s.fill(s.Cells[end-n*s.Cols : end])
	} else if n < 0 {
		n = -n
		copy(s.Cells[start+n*s.Cols:end], s.Cells[start:end-n*s.Cols])
		s.fill(s.Cells[start : start+n*s.Cols])
	}
}
func (s *Screen) linefeed() {
	s.wrap = false
	if s.y == s.bottom {
		s.scroll(1)
	} else {
		s.y = min(s.y+1, s.Rows-1)
	}
}
func (s *Screen) reverseIndex() {
	s.wrap = false
	if s.y == s.top {
		s.scroll(-1)
	} else {
		s.y = max(0, s.y-1)
	}
}

// RuneWidth follows terminal columns for combining marks and East Asian text.
func RuneWidth(r rune) int {
	if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || r == 0x200d || r >= 0xfe00 && r <= 0xfe0f {
		return 0
	}
	if r >= 0x1100 && (r <= 0x115f || r == 0x2329 || r == 0x232a || r >= 0x2e80 && r <= 0xa4cf && r != 0x303f || r >= 0xac00 && r <= 0xd7a3 || r >= 0xf900 && r <= 0xfaff || r >= 0xfe10 && r <= 0xfe19 || r >= 0xfe30 && r <= 0xfe6f || r >= 0xff00 && r <= 0xff60 || r >= 0xffe0 && r <= 0xffe6 || r >= 0x1f300 && r <= 0x1faff || r >= 0x20000 && r <= 0x3ffff) {
		return 2
	}
	return 1
}
func (s *Screen) eraseGlyph(i int) {
	if s.Cells[i].Width == 0 && i%s.Cols > 0 {
		s.Cells[i-1] = s.blank()
	}
	if s.Cells[i].Width == 2 && i%s.Cols < s.Cols-1 {
		s.Cells[i+1] = s.blank()
	}
	s.Cells[i] = s.blank()
}
func (s *Screen) put(r rune) {
	if s.charsets[s.activeCharset] && r >= 0x60 && r <= 0x7e {
		r = []rune("◆▒␉␌␍␊°±␤␋┘┐┌└┼⎺⎻─⎼⎽├┤┴┬│≤≥π≠£·")[r-0x60]
	}
	w := RuneWidth(r)
	if w == 0 {
		i := s.y*s.Cols + s.x
		if !s.wrap {
			i--
		}
		if i >= 0 {
			if s.Cells[i].Width == 0 && i > 0 {
				i--
			}
			for j := range s.Cells[i].Combining {
				if s.Cells[i].Combining[j] == 0 {
					s.Cells[i].Combining[j] = r
					break
				}
			}
		}
		return
	}
	if s.wrap || w == 2 && s.x == s.Cols-1 {
		if s.autowrap {
			s.x = 0
			s.linefeed()
		}
		s.wrap = false
	}
	if w == 2 && s.x == s.Cols-1 {
		return
	}
	i := s.y*s.Cols + s.x
	if s.insert {
		row := s.Cells[s.y*s.Cols : (s.y+1)*s.Cols]
		copy(row[s.x+w:], row[s.x:s.Cols-w])
	}
	s.eraseGlyph(i)
	if w == 2 {
		s.eraseGlyph(i + 1)
	}
	c := s.style
	c.Rune = r
	c.Width = uint8(w)
	c.Combining = [3]rune{}
	s.Cells[i] = c
	if w == 2 {
		c.Rune = 0
		c.Width = 0
		s.Cells[i+1] = c
	}
	s.x += w
	if s.x >= s.Cols {
		s.x = s.Cols - 1
		s.wrap = s.autowrap
	}
}
func (s *Screen) save() { s.saved = cursor{s.x, s.y, s.style, s.wrap} }
func (s *Screen) restore() {
	s.x, s.y = min(s.saved.x, s.Cols-1), min(s.saved.y, s.Rows-1)
	s.style, s.wrap = s.saved.style, s.saved.wrap
}
func (s *Screen) alternateMode(on bool, save bool) {
	if on == s.alternate {
		return
	}
	if on {
		s.primary, s.Cells = s.Cells, s.primary
		if len(s.Cells) != s.Cols*s.Rows {
			s.Cells = make([]Cell, s.Cols*s.Rows)
		}
		s.fill(s.Cells)
		s.primaryCursor = cursor{s.x, s.y, s.style, s.wrap}
		s.x, s.y = 0, 0
	} else {
		s.Cells, s.primary = s.primary, s.Cells
		if save {
			c := s.primaryCursor
			s.x, s.y = min(c.x, s.Cols-1), min(c.y, s.Rows-1)
			s.style = c.style
		}
	}
	s.alternate = on
	s.top, s.bottom = 0, s.Rows-1
	s.wrap = false
}
func (s *Screen) control(b byte) {
	switch b {
	case '\r':
		s.x = 0
		s.wrap = false
	case '\n', '\v', '\f':
		s.linefeed()
	case '\b':
		s.x = max(0, s.x-1)
		s.wrap = false
	case 14:
		s.activeCharset = 1
	case 15:
		s.activeCharset = 0
	case '\t':
		s.x = min((s.x/8+1)*8, s.Cols-1)
		s.wrap = false
	}
}
func (s *Screen) Feed(data []byte) {
	if len(data) == 0 {
		return
	}
	s.Revision++
	for _, b := range data {
		if s.state == 3 || s.state == 4 || s.state == 5 || s.state == 6 {
			if b == 0x18 || b == 0x1a {
				s.state = 0
				s.length = 0
				continue
			}
			if (s.state == 3 && b == 7) || ((s.state == 4 || s.state == 6) && b == '\\') {
				if s.state != 6 && !s.sequenceOverflow {
					s.osc()
				}
				s.state = 0
				s.length = 0
				continue
			}
			if b == 27 {
				if s.state == 3 || s.state == 4 {
					s.state = 4
				} else {
					s.state = 6
				}
				continue
			}
			if s.state == 4 {
				s.state = 3
			}
			if s.state == 6 {
				s.state = 5
			}
			if s.state == 3 {
				if s.length < len(s.sequence) {
					s.sequence[s.length] = b
					s.length++
				} else {
					s.sequenceOverflow = true
				}
			}
			continue
		}
		if b == 27 {
			s.state = 1
			s.length = 0
			s.pendingLen = 0
			s.sequenceOverflow = false
			continue
		}
		if b < 32 || b == 127 {
			if b == 0x18 || b == 0x1a {
				s.state = 0
			} else {
				s.control(b)
			}
			continue
		}
		switch s.state {
		case 1:
			s.state = 0
			switch b {
			case '[':
				s.state = 2
			case ']':
				s.state = 3
			case 'P', '^', '_':
				s.state = 5
			case '7':
				s.save()
			case '8':
				s.restore()
			case 'D':
				s.linefeed()
			case 'E':
				s.x = 0
				s.linefeed()
			case 'M':
				s.reverseIndex()
			case 'c':
				s.style = Cell{Foreground: Default, Background: Default}
				s.alternateMode(false, true)
				s.Clear()
				s.x, s.y = 0, 0
				s.top, s.bottom = 0, s.Rows-1
				s.autowrap = true
				s.CursorVisible = true
				s.ApplicationCursor = false
				s.BracketedPaste = false
				s.origin = false
				s.insert = false
				s.MouseTracking = 0
				s.MouseSGR = false
				s.charsets = [2]bool{}
				s.activeCharset = 0
			case '(', ')', '*', '+':
				s.designateCharset = 0
				if b == ')' {
					s.designateCharset = 1
				}
				s.state = 7
			}
		case 7:
			s.charsets[s.designateCharset] = b == '0'
			s.state = 0
		case 2:
			if b >= 0x40 && b <= 0x7e {
				if !s.sequenceOverflow {
					s.csi(b)
				}
				s.state = 0
				s.length = 0
			} else if s.length < len(s.sequence) {
				s.sequence[s.length] = b
				s.length++
			} else {
				s.sequenceOverflow = true
			}
		default:
			if s.pendingLen == 0 && b < utf8.RuneSelf {
				s.put(rune(b))
				continue
			}
			if s.pendingLen > 0 && b&0xc0 != 0x80 {
				s.put(utf8.RuneError)
				s.pendingLen = 0
			}
			s.pending[s.pendingLen] = b
			s.pendingLen++
			if utf8.FullRune(s.pending[:s.pendingLen]) {
				r, _ := utf8.DecodeRune(s.pending[:s.pendingLen])
				s.put(r)
				s.pendingLen = 0
			}
		}
	}
}
func (s *Screen) osc() {
	p := s.sequence[:s.length]
	for i, b := range p {
		if b != ';' {
			continue
		}
		if i == 1 && (p[0] == '0' || p[0] == '2') {
			s.Title = string(p[i+1:])
		}
		// Modern TUIs ask for the host's actual default colors during startup.
		if i == 2 && len(p) == 4 && p[0] == '1' && (p[1] == '0' || p[1] == '1') && p[3] == '?' {
			c := s.DefaultForeground
			if p[1] == '1' {
				c = s.DefaultBackground
			}
			s.reply = append(s.reply, 27, ']', p[0], p[1], ';')
			s.reply = append(s.reply, "rgb:"...)
			const hex = "0123456789abcdef"
			for shift := 16; shift >= 0; shift -= 8 {
				v := byte(c >> shift)
				s.reply = append(s.reply, hex[v>>4], hex[v&15], hex[v>>4], hex[v&15])
				if shift > 0 {
					s.reply = append(s.reply, '/')
				}
			}
			s.reply = append(s.reply, 27, '\\')
		}
		return
	}
}

func (s *Screen) csi(final byte) {
	p := s.sequence[:s.length]
	secondary := len(p) > 0 && p[0] == '>'
	private := len(p) > 0 && p[0] == '?'
	if private || secondary {
		p = p[1:]
	}
	clear(s.params[:])
	n := 1
	for _, b := range p {
		if b >= '0' && b <= '9' {
			s.params[n-1] = min(100000, s.params[n-1]*10+int(b-'0'))
		} else if b == ';' || b == ':' {
			if n == len(s.params) {
				return
			}
			n++
		} else {
			return
		}
	}
	a := s.params[:n]
	v := max(1, a[0])
	if final != 'm' && final != 'n' && final != 'c' && final != 's' && final != 'u' && final != 'h' && final != 'l' {
		s.wrap = false
	}
	low, high := 0, s.Rows-1
	if s.origin {
		low, high = s.top, s.bottom
	}
	switch final {
	case 'A':
		s.y = max(low, s.y-v)
	case 'B', 'e':
		s.y = min(high, s.y+v)
	case 'C', 'a':
		s.x = min(s.Cols-1, s.x+v)
	case 'D':
		s.x = max(0, s.x-v)
	case 'E':
		s.x = 0
		s.y = min(high, s.y+v)
	case 'F':
		s.x = 0
		s.y = max(low, s.y-v)
	case 'G', '`':
		s.x = min(s.Cols-1, v-1)
	case 'I':
		s.x = min(s.Cols-1, (s.x/8+v)*8)
	case 'Z':
		s.x = max(0, ((max(0, s.x-1))/8-v+1)*8)
	case 'd':
		s.y = min(high, low+v-1)
	case 'H', 'f':
		x := 1
		if n > 1 {
			x = max(1, a[1])
		}
		s.x = min(s.Cols-1, x-1)
		s.y = min(high, low+v-1)
	case 'J':
		switch a[0] {
		case 0:
			s.fill(s.Cells[s.y*s.Cols+s.x:])
		case 1:
			s.fill(s.Cells[:s.y*s.Cols+s.x+1])
		case 2:
			s.fill(s.Cells)
		case 3:
			s.History = s.History[:0]
			s.historyHead = 0
		}
	case 'K':
		start, end := s.y*s.Cols, (s.y+1)*s.Cols
		switch a[0] {
		case 0:
			start += s.x
		case 1:
			end = start + s.x + 1
		}
		s.fill(s.Cells[start:end])
	case 'm':
		s.sgr(a)
	case 's':
		s.save()
	case 'u':
		s.restore()
	case 'r':
		bottom := s.Rows
		if n > 1 && a[1] > 0 {
			bottom = a[1]
		}
		if v < bottom && bottom <= s.Rows {
			s.top, s.bottom = v-1, bottom-1
			s.x, s.y = 0, 0
			if s.origin {
				s.y = s.top
			}
		}
	case 'S':
		s.scroll(v)
	case 'T':
		s.scroll(-v)
	case 'L', 'M':
		if s.y >= s.top && s.y <= s.bottom {
			top := s.top
			s.top = s.y
			if final == 'L' {
				s.scroll(-v)
			} else {
				s.scroll(v)
			}
			s.top = top
		}
	case '@', 'P', 'X':
		row := s.Cells[s.y*s.Cols : (s.y+1)*s.Cols]
		v = min(v, s.Cols-s.x)
		switch final {
		case '@':
			copy(row[s.x+v:], row[s.x:s.Cols-v])
			s.fill(row[s.x : s.x+v])
		case 'P':
			copy(row[s.x:], row[s.x+v:])
			s.fill(row[s.Cols-v:])
		case 'X':
			s.fill(row[s.x : s.x+v])
		}
	case 'h', 'l':
		on := final == 'h'
		for _, mode := range a {
			if private {
				switch mode {
				case 1:
					s.ApplicationCursor = on
				case 6:
					s.origin = on
					s.x, s.y = 0, 0
					if on {
						s.y = s.top
					}
				case 7:
					s.autowrap = on
				case 25:
					s.CursorVisible = on
				case 47, 1047, 1049:
					s.alternateMode(on, mode == 1049)
				case 1048:
					if on {
						s.save()
					} else {
						s.restore()
					}
				case 1000, 1002, 1003:
					if on {
						s.MouseTracking = mode
					} else if s.MouseTracking == mode {
						s.MouseTracking = 0
					}
				case 1006:
					s.MouseSGR = on
				case 2004:
					s.BracketedPaste = on
				}
			} else if mode == 4 {
				s.insert = on
			}
		}
	case 'n':
		if a[0] == 5 {
			s.reply = append(s.reply, "\x1b[0n"...)
		} else if a[0] == 6 {
			s.reply = append(s.reply, "\x1b["...)
			s.reply = strconv.AppendInt(s.reply, int64(s.y+1), 10)
			s.reply = append(s.reply, ';')
			s.reply = strconv.AppendInt(s.reply, int64(s.x+1), 10)
			s.reply = append(s.reply, 'R')
		}
	case 'c':
		if secondary {
			s.reply = append(s.reply, "\x1b[>0;1;0c"...)
		} else {
			s.reply = append(s.reply, "\x1b[?1;2c"...)
		}
	}
}

var Palette = [16]Color{0x1e1e1e, 0xcd3131, 0x0dbc79, 0xe5e510, 0x2472c8, 0xbc3fbc, 0x11a8cd, 0xe5e5e5, 0x666666, 0xf14c4c, 0x23d18b, 0xf5f543, 0x3b8eea, 0xd670d6, 0x29b8db, 0xffffff}

func IndexedColor(n int) Color {
	n = max(0, min(n, 255))
	if n < 16 {
		return Palette[n]
	}
	if n >= 232 {
		v := Color(8 + (n-232)*10)
		return v<<16 | v<<8 | v
	}
	n -= 16
	level := func(x int) Color {
		if x == 0 {
			return 0
		}
		return Color(55 + 40*x)
	}
	return level(n/36)<<16 | level(n/6%6)<<8 | level(n%6)
}
func (s *Screen) sgr(a []int) {
	for i := 0; i < len(a); i++ {
		v := a[i]
		switch {
		case v == 0:
			s.style = Cell{Foreground: Default, Background: Default}
		case v == 1:
			s.style.Bold = true
		case v == 4:
			s.style.Underline = true
		case v == 7:
			s.style.Inverse = true
		case v == 22:
			s.style.Bold = false
		case v == 24:
			s.style.Underline = false
		case v == 27:
			s.style.Inverse = false
		case v == 39:
			s.style.Foreground = Default
		case v == 49:
			s.style.Background = Default
		case v >= 30 && v <= 37:
			s.style.Foreground = Palette[v-30]
		case v >= 40 && v <= 47:
			s.style.Background = Palette[v-40]
		case v >= 90 && v <= 97:
			s.style.Foreground = Palette[v-90+8]
		case v >= 100 && v <= 107:
			s.style.Background = Palette[v-100+8]
		case v == 38 || v == 48:
			color := Default
			if i+2 < len(a) && a[i+1] == 5 {
				color = IndexedColor(a[i+2])
				i += 2
			} else if i+4 < len(a) && a[i+1] == 2 {
				color = Color(min(255, a[i+2]))<<16 | Color(min(255, a[i+3]))<<8 | Color(min(255, a[i+4]))
				i += 4
			}
			if v == 38 {
				s.style.Foreground = color
			} else {
				s.style.Background = color
			}
		}
	}
}
