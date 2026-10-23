package workbench

import (
	"github.com/diiyw/onix/scene"
	"github.com/diiyw/onix/syntax"
)

// Cache paint spans and bracket offsets, never per-tab trees. Brackets count
// toward the same slot budget as spans, keeping the FIFO below 2 MiB.
const (
	pencodeHighlightCacheSpanLimit  = 65536
	pencodeHighlightCacheSpareLimit = 4096
)

type pencodeHighlightCacheEntry struct {
	file        *openFile
	source      string
	theme       *syntax.Theme
	definitions *syntax.Definitions
	spans       []scene.TextSpan2D
	brackets    []pencodeBracket
	lines       int
	characters  int
	lineInfo    string
	valid       bool
}

type pencodeHighlightCache struct {
	entries [64]pencodeHighlightCacheEntry
	next    int
	spans   int
	spare   []scene.TextSpan2D // One small buffer for close/reopen and preview replacement.
}

func (cache *pencodeHighlightCache) get(file *openFile, theme *syntax.Theme, definitions *syntax.Definitions) (*pencodeHighlightCacheEntry, bool) {
	if cache != nil {
		entry := &cache.entries[file.highlightSlot]
		if entry.file == file {
			return entry, entry.valid && entry.source == file.Content && entry.theme == theme && entry.definitions == definitions
		}
	}
	return nil, false
}

func (cache *pencodeHighlightCache) remove(file *openFile) {
	if cache != nil {
		entry := &cache.entries[file.highlightSlot]
		if entry.file == file {
			cache.spans -= cap(entry.spans) + cap(entry.brackets)
			if capacity := cap(entry.spans); capacity <= pencodeHighlightCacheSpareLimit && capacity > cap(cache.spare) {
				cache.spare = entry.spans[:0]
			}
			*entry = pencodeHighlightCacheEntry{}
		}
	}
}

// Keep the bounded paint buffer for the next edit, but release the old source
// immediately. Closing a file removes all its references and may recycle one
// small paint buffer, which contains no pointers to file content.
func (cache *pencodeHighlightCache) invalidate(file *openFile) {
	if cache != nil {
		entry := &cache.entries[file.highlightSlot]
		if entry.file == file {
			entry.source, entry.lineInfo, entry.valid = "", "", false
			entry.theme, entry.definitions = nil, nil
		}
	}
}

func (cache *pencodeHighlightCache) put(value pencodeHighlightCacheEntry) {
	if len(value.spans)+len(value.brackets) > pencodeHighlightCacheSpanLimit {
		cache.remove(value.file)
		return
	}
	slot := cache.next
	if cache.entries[value.file.highlightSlot].file == value.file {
		slot = int(value.file.highlightSlot)
	}
	entry := &cache.entries[slot]
	spans := entry.spans
	brackets := entry.brackets
	cache.spans -= cap(spans) + cap(brackets)
	*entry = pencodeHighlightCacheEntry{}
	capacity := len(value.spans)
	if cap(spans) < capacity && cap(cache.spare) >= capacity {
		spans, cache.spare = cache.spare, nil
	}
	if cap(spans) >= capacity {
		capacity = cap(spans)
	} else {
		spans = nil
	}
	bracketCapacity := len(value.brackets)
	if cap(brackets) >= bracketCapacity {
		bracketCapacity = cap(brackets)
	} else {
		brackets = nil
	}
	// A smaller replacement can retain two independently oversized buffers.
	if capacity+bracketCapacity > pencodeHighlightCacheSpanLimit {
		spans, brackets = nil, nil
		capacity, bracketCapacity = len(value.spans), len(value.brackets)
	}
	if cache.spans+capacity+bracketCapacity+cap(cache.spare) > pencodeHighlightCacheSpanLimit {
		cache.spare = nil
	}
	for cache.spans+capacity+bracketCapacity > pencodeHighlightCacheSpanLimit {
		victim := &cache.entries[cache.next]
		cache.spans -= cap(victim.spans) + cap(victim.brackets)
		*victim = pencodeHighlightCacheEntry{}
		cache.next = (cache.next + 1) % len(cache.entries)
	}
	if spans == nil && capacity > 0 {
		spans = make([]scene.TextSpan2D, capacity)
	}
	spans = spans[:len(value.spans)]
	copy(spans, value.spans)
	value.spans = spans
	if brackets == nil && bracketCapacity > 0 {
		brackets = make([]pencodeBracket, bracketCapacity)
	}
	brackets = brackets[:len(value.brackets)]
	copy(brackets, value.brackets)
	value.brackets = brackets
	value.valid = true
	*entry = value
	value.file.highlightSlot = uint8(slot)
	cache.spans += capacity + bracketCapacity
	if cache.next == slot {
		cache.next = (cache.next + 1) % len(cache.entries)
	}
}
