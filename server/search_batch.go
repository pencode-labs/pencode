package server

import (
	"bytes"
	"context"
	"fmt"

	"github.com/pencode-labs/pencode/textsearch"
)

type searchBuffer struct {
	path       string
	start, end int
}

// All offsets, line previews and digests in a batch refer to the same snapshot.
// NUL cannot occur in a validated text document.
type literalSearchBatch struct {
	docs []searchBuffer
	data []byte
}

func (b *literalSearchBatch) append(path string, content []byte) {
	start := len(b.data)
	b.data = append(b.data, content...)
	b.docs = append(b.docs, searchBuffer{path, start, len(b.data)})
	b.data = append(b.data, 0)
}

func (b *literalSearchBatch) search(ctx context.Context, p *textsearch.Pattern, result *searchResults) error {
	if len(b.docs) == 0 {
		return nil
	}
	current, count := -1, 0
	matches := result.scratch[:0]
	finish := func() error {
		if count == 0 {
			return nil
		}
		doc := b.docs[current]
		err := result.record(doc.path, b.data[doc.start:doc.end], matches, count)
		count, matches = 0, matches[:0]
		return err
	}
	err := p.LiteralMatches(ctx, bytes.NewReader(b.data), func(i, start, end int) error {
		if i >= len(b.docs) || i < current {
			return fmt.Errorf("invalid ripgrep document number %d", i)
		}
		if i != current {
			if err := finish(); err != nil {
				return err
			}
			current = i
		}
		doc := b.docs[i]
		if start < doc.start || end > doc.end {
			return fmt.Errorf("invalid ripgrep match range in %q", doc.path)
		}
		start, end = start-doc.start, end-doc.start
		if p.WordBoundary(b.data[doc.start:doc.end], start, end) {
			count++
			if len(matches) < result.remaining() {
				matches = append(matches, textsearch.Match{Start: start, End: end})
			}
		}
		return nil
	})
	if err == nil {
		err = finish()
	}
	clear(b.docs)
	b.docs, b.data = b.docs[:0], b.data[:0]
	result.scratch = matches
	return err
}
