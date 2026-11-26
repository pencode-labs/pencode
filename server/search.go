package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/pencode-labs/pencode/internal/buffer"
	"github.com/pencode-labs/pencode/internal/ripgrep"
	"github.com/pencode-labs/pencode/protocol"
	"github.com/pencode-labs/pencode/textsearch"
)

func (s *services) searchText(ctx context.Context, root string, d *protocol.Decoder, e *protocol.Encoder) error {
	directory, query, flags, count := d.String(), d.String(), d.U32(), d.U32()
	if directory == "" {
		directory = "."
	}
	if !fs.ValidPath(directory) || flags > 15 || count > 4096 {
		return protocol.ErrProtocol
	}
	documents := make(map[string][]byte, count)
	for range count {
		name, content := d.String(), d.Bytes()
		if !fs.ValidPath(name) {
			return protocol.ErrProtocol
		}
		documents[name] = content
	}
	if err := d.Done(); err != nil {
		return err
	}
	if query == "" || strings.IndexByte(query, 0) >= 0 && flags&4 == 0 {
		e.U32(0)
		e.U32(0)
		e.U32(0)
		return nil
	}
	p, err := textsearch.Compile(query, protocol.SearchOptions(flags))
	if err != nil {
		return err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	var data *buffer.Buffer
	defer func() { buffer.Put(data) }()
	result := searchResults{budget: 12}
	var batch literalSearchBatch
	add := func(name string, content []byte) error {
		if bytes.IndexByte(content, 0) >= 0 || !utf8.Valid(content) {
			result.Skipped++
			return nil
		}
		if !p.Literal() {
			var count int
			result.scratch, count = p.FindRange(content, result.scratch, 0, len(content), result.remaining(), true)
			return result.record(name, content, result.scratch, count)
		}
		batch.append(name, content)
		if len(batch.data) >= protocol.TextSearchFileLimit || len(batch.docs) >= 256 {
			return batch.search(ctx, p, &result)
		}
		return nil
	}
	// Walk from the registered workspace root. Only ripgrep's glob receives
	// the requested scope, so a directory symlink never becomes an explicit
	// command-line path that ripgrep would follow outside the workspace.
	excludePrefix := "!"
	args := []string{"--hidden", "--no-ignore", "--null", "--path-separator=/", "--max-filesize", strconv.Itoa(protocol.TextSearchFileLimit)}
	if directory != "." {
		local, err := filepath.Localize(directory)
		if err != nil {
			return err
		}
		info, err := r.Lstat(local)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("invalid search directory %q", directory)
		}
		prefix := "/" + globEscape.Replace(directory) + "/**"
		args = append(args, "--glob", prefix)
		excludePrefix = "!" + prefix + "/"
	}
	args = append(args, "--glob", excludePrefix+".git", "--glob", excludePrefix+"dist", "--glob", excludePrefix+"node_modules", "--iglob", protocol.BinarySearchGlob)
	literal, sensitive := p.DiskFilter()
	if literal == "" {
		args = append(args, "--files")
	} else {
		args = append(args, "--files-with-matches", "--multiline")
		args = append(args, textsearch.LiteralArgs(literal, sensitive)...)
	}
	args = append(args, "--", ".")
	err = ripgrep.Run(ctx, root, nil, args, func(out *bufio.Reader) error {
		return ripgrep.Records(out, func(record []byte) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			name := strings.TrimPrefix(string(record), "./")
			if _, open := documents[name]; open {
				return nil
			}
			if !fs.ValidPath(name) || directory != "." && !strings.HasPrefix(name, directory+"/") {
				return protocol.ErrProtocol
			}
			f, err := r.Open(filepath.FromSlash(name))
			if err != nil {
				result.Skipped++
				return nil
			}
			defer f.Close()
			info, err := f.Stat()
			if err != nil || !info.Mode().IsRegular() || info.Size() > protocol.TextSearchFileLimit {
				result.Skipped++
				return nil
			}
			if data == nil {
				data = buffer.Get(int(info.Size()) + 1)
			}
			if err := data.ReadAll(f, int(info.Size()), protocol.TextSearchFileLimit); err != nil {
				result.Skipped++
				return nil
			}
			return add(name, data.Bytes)
		})
	})
	if err != nil {
		return err
	}
	// An open document replaces its disk version even when only one matches.
	for name, content := range documents {
		if err := ctx.Err(); err != nil {
			return err
		}
		if (directory == "." || strings.HasPrefix(name, directory+"/")) && !protocol.IsBinaryFile(name) {
			if err := add(name, content); err != nil {
				return err
			}
		}
	}
	if err := batch.search(ctx, p, &result); err != nil {
		return err
	}
	result.encode(e)
	return nil
}

var globEscape = strings.NewReplacer(`\`, `\\`, "*", `\*`, "?", `\?`, "[", `\[`, "]", `\]`, "{", `\{`, "}", `\}`)

type searchResults struct {
	scratch []textsearch.Match
	protocol.SearchResult
	shown, budget int
}

func (result *searchResults) remaining() int { return protocol.TextSearchLimit - result.shown }

func (result *searchResults) record(name string, content []byte, displayed []textsearch.Match, count int) error {
	if count == 0 {
		return nil
	}
	if len(result.Files) == protocol.TextSearchFilesLimit {
		return fmt.Errorf("搜索文件过多，请缩小文件夹范围")
	}
	if count+result.shown > protocol.TextSearchLimit {
		result.Truncated = true
	}
	digest := sha256.Sum256(content)
	file := protocol.SearchFile{Path: name, Digest: string(digest[:]), Count: count}
	line, offset, lineStart := 1, 0, 0
	for _, match := range displayed {
		for offset < match.Start {
			if content[offset] == '\n' {
				line++
				lineStart = offset + 1
			}
			offset++
		}
		end := bytes.IndexByte(content[match.Start:], '\n')
		if end < 0 {
			end = len(content)
		} else {
			end += match.Start
		}
		start := max(lineStart, match.Start-80)
		for start > lineStart && !utf8.RuneStart(content[start]) {
			start--
		}
		end = min(end, start+320)
		for end < len(content) && !utf8.RuneStart(content[end]) {
			end--
		}
		file.Matches = append(file.Matches, protocol.TextMatch{Start: match.Start, End: match.End, Line: line, Column: utf8.RuneCount(content[lineStart:match.Start]) + 1, PreviewStart: start, Preview: string(bytes.TrimSuffix(content[start:end], []byte("\r")))})
	}
	result.shown += len(displayed)
	result.budget += len(file.Path) + len(file.Digest) + 16
	for _, match := range file.Matches {
		result.budget += 24 + len(match.Preview)
	}
	if result.budget > protocol.MaxMessage-4096 {
		return fmt.Errorf("搜索结果过大，请缩小文件夹范围")
	}
	result.Files = append(result.Files, file)
	return nil
}

func (result *searchResults) encode(e *protocol.Encoder) {
	slices.SortFunc(result.Files, func(a, b protocol.SearchFile) int { return strings.Compare(a.Path, b.Path) })
	if result.Truncated {
		e.U32(1)
	} else {
		e.U32(0)
	}
	e.U32(uint32(result.Skipped))
	e.U32(uint32(len(result.Files)))
	for _, f := range result.Files {
		e.String(f.Path)
		e.String(f.Digest)
		e.U32(uint32(f.Count))
		e.U32(uint32(len(f.Matches)))
		for _, m := range f.Matches {
			e.U32(uint32(m.Start))
			e.U32(uint32(m.End))
			e.U32(uint32(m.Line))
			e.U32(uint32(m.Column))
			e.U32(uint32(m.PreviewStart))
			e.String(m.Preview)
		}
	}
}
