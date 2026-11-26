package textsearch

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/onix-engine/onix/text/piecetree"
	"github.com/pencode-labs/pencode/internal/ripgrep"
)

// DiskFilter returns a necessary substring for a match. The final scan of
// each candidate uses the same bytes for offsets, previews and the save digest.
// For regexps with no known mandatory substring every file is a candidate.
func (p *Pattern) DiskFilter() (literal string, caseSensitive bool) {
	if p.re == nil || p.literal {
		return p.query, p.options.CaseSensitive
	}
	return p.diskLiteral, !p.diskFold
}

func LiteralArgs(query string, sensitive bool) []string {
	args := []string{"--text", "--encoding=none"}
	if !sensitive {
		args = append(args, "--ignore-case")
	}
	// A command line cannot contain NUL. Encode it in a literal regexp.
	if strings.IndexByte(query, 0) >= 0 {
		return append(args, "-e", strings.ReplaceAll(regexp.QuoteMeta(query), "\x00", `\x00`))
	}
	return append(args, "--fixed-strings", "-e", query)
}

// Literal reports whether this pattern uses plain substring matching.
func (p *Pattern) Literal() bool { return p.re == nil || p.literal }

// LiteralMatches streams NUL-separated documents through one process. rg emits
// a one-based record number and a byte offset relative to the entire input.
func (p *Pattern) LiteralMatches(ctx context.Context, source io.Reader, visit func(document, start, end int) error) error {
	args := append(LiteralArgs(p.query, p.options.CaseSensitive), "--null-data", "--only-matching", "--byte-offset", "--line-number", "--", "-")
	return ripgrep.Run(ctx, "", source, args, func(r *bufio.Reader) error {
		return ripgrep.Records(r, func(record []byte) error {
			number, tail, ok := bytes.Cut(record, []byte{':'})
			offset, value, found := bytes.Cut(tail, []byte{':'})
			document, numberErr := strconv.Atoi(string(number))
			start, offsetErr := strconv.Atoi(string(offset))
			if !ok || !found || numberErr != nil || offsetErr != nil || document < 1 || start < 0 {
				return errors.New("invalid ripgrep match offset")
			}
			return visit(document-1, start, start+len(value))
		})
	})
}

// Short literal queries on small snapshots cost less to scan than starting rg.
// Keep long needles on rg's matcher to bound repeated-prefix comparisons.
const localFindBytes = 64 << 10
const localFindQueryBytes = 64

var errMatchLimit = errors.New("match limit reached")

// SearchSnapshot reads the edited pieces directly. Regex replacements need
// capture offsets and whole-document assertions from the compiled pattern.
func (p *Pattern) SearchSnapshot(ctx context.Context, content piecetree.Snapshot, dst []Match, start, end, limit int) ([]Match, error) {
	if p == nil || p.query == "" || limit == 0 {
		return dst[:0], nil
	}
	if err := ctx.Err(); err != nil {
		return dst[:0], err
	}
	if !p.Literal() || strings.IndexByte(p.query, 0) >= 0 || content.Len() <= localFindBytes && len(p.query) <= localFindQueryBytes {
		return p.FindSnapshot(content, dst, start, end, limit), ctx.Err()
	}
	clear(dst)
	dst = dst[:0]
	source := io.NewSectionReader(content, 0, int64(end))
	err := p.LiteralMatches(ctx, source, func(_ int, first, last int) error {
		if last > end {
			return errors.New("ripgrep offset exceeds document length")
		}
		if first < start {
			return nil
		}
		if p.options.WholeWord {
			left, _ := snapshotLastRune(content, first)
			right, _ := content.DecodeRune(last)
			if first > 0 && word(left) || last < content.Len() && word(right) {
				return nil
			}
		}
		dst = append(dst, Match{Start: first, End: last})
		if limit > 0 && len(dst) == limit {
			return errMatchLimit
		}
		return nil
	})
	if errors.Is(err, errMatchLimit) {
		err = nil
	}
	return dst, err
}
