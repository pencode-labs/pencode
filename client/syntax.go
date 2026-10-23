package client

import (
	"context"
	"slices"
	"time"

	"github.com/diiyw/onix/projects/pencode/protocol"
	"github.com/diiyw/onix/syntax"
)

// Syntax owns a server parser session. Updates transmit only the changed range.
// Like syntax.Document it belongs to one worker, never the render thread.
type Syntax struct {
	context          context.Context
	path             string
	initialized      bool
	client           *Client
	id, version      uint64
	source, language string
	captures         []syntax.Token
	names            []string
	painter          syntax.Painter
}

func (c *Client) NewSyntax(ctx context.Context) *Syntax {
	if c == nil {
		return &Syntax{context: ctx}
	}
	c.mu.Lock()
	c.next++
	id := c.next
	c.mu.Unlock()
	return &Syntax{client: c, id: id, context: ctx}
}
func (s *Syntax) Update(path, source string) error {
	if s.initialized && s.path == path && s.source == source {
		return nil
	}
	ctx, cancel := context.WithTimeout(s.context, 30*time.Second)
	defer cancel()
	start, end, next := changedRange(s.source, source)
	e := service(protocol.SyntaxAnalyze, "")
	e.U64(s.id)
	e.U64(s.version)
	e.String(path)
	e.U32(uint32(start))
	e.U32(uint32(end))
	e.String(source[start:next])
	d, err := s.client.execute(ctx, &e)
	if err != nil {
		s.Close()
		return err
	}
	version, language := d.U64(), d.String()
	if err := s.applyCaptureDelta(&d, source); err != nil {
		s.Close()
		return err
	}
	s.version, s.source, s.language = version, source, language
	s.path, s.initialized = path, true
	return nil
}
func (s *Syntax) Tokens(theme *syntax.Theme, definitions *syntax.Definitions) []syntax.Token {
	return s.painter.Tokens(s.language, s.captures, theme, definitions)
}
func (s *Syntax) Close() { s.Release(false) }
func (s *Syntax) Release(trim bool) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	e := service(protocol.SyntaxRelease, "")
	e.U64(s.id)
	if trim {
		e.U32(1)
	} else {
		e.U32(0)
	}
	_, _ = s.client.execute(ctx, &e)
	s.version = 0
	s.source = ""
	s.path, s.language, s.initialized = "", "", false
	s.captures = nil
	s.names = nil
	s.painter = syntax.Painter{}
}

func (s *Syntax) Language() string { return s.language }

func (s *Syntax) applyCaptureDelta(d *protocol.Decoder, source string) error {
	prefix, removed, count := int(d.U32()), int(d.U32()), int(d.U32())
	if d.Err != nil || prefix > len(s.captures) || removed > len(s.captures)-prefix || count > len(d.Buffer)/12 {
		return protocol.ErrProtocol
	}
	oldEnd := prefix + removed
	oldLen := len(s.captures)
	total := oldLen - removed + count
	shift := len(source) - len(s.source)
	s.captures = slices.Grow(s.captures, max(0, total-oldLen))
	// Move the retained suffix before decoding into its replacement gap. On a
	// malformed response Update releases the session, so no second full-sized
	// capture array is needed to stage the incoming ranges.
	s.captures = s.captures[:max(total, oldLen)]
	copy(s.captures[prefix+count:], s.captures[oldEnd:oldLen])
	s.captures = s.captures[:total]
	last := 0
	for _, token := range s.captures[:prefix] {
		if token.Start < last || token.End > len(source) || token.End <= token.Start {
			return protocol.ErrProtocol
		}
		last = token.End
	}
	for i := prefix; i < prefix+count; i++ {
		token := syntax.Token{Start: int(d.U32()), End: int(d.U32())}
		id := int(d.U32())
		if id > len(s.names) {
			return protocol.ErrProtocol
		}
		if id == len(s.names) {
			s.names = append(s.names, d.String())
		}
		token.Capture = s.names[id]
		if token.Start < last || token.Start >= token.End || token.End > len(source) {
			return protocol.ErrProtocol
		}
		last = token.End
		s.captures[i] = token
	}
	if err := d.Done(); err != nil {
		return err
	}
	for i := prefix + count; i < total; i++ {
		token := &s.captures[i]
		token.Start += shift
		token.End += shift
		if token.Start < last || token.End > len(source) || token.End <= token.Start {
			return protocol.ErrProtocol
		}
		last = token.End
	}
	clear(s.captures[total:max(total, oldLen)])
	return nil
}
