package server

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

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
	documents := make(map[string]string, count)
	for range count {
		name, content := d.String(), d.String()
		if !fs.ValidPath(name) {
			return protocol.ErrProtocol
		}
		documents[name] = content
	}
	if err := d.Done(); err != nil {
		return err
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
	var result protocol.SearchResult
	var matches []textsearch.Match
	total := 0
	budget := 12
	var searchErr error
	add := func(name, content string) bool {
		if strings.IndexByte(content, 0) >= 0 || !utf8.ValidString(content) {
			result.Skipped++
			return true
		}
		var count int
		matches, count = p.FindRange(content, matches, 0, len(content), protocol.TextSearchLimit-total, true)
		if count == 0 {
			return true
		}
		if len(result.Files) == protocol.TextSearchFilesLimit {
			searchErr = fmt.Errorf("搜索文件过多，请缩小文件夹范围")
			return false
		}
		displayed := matches
		if count+total > protocol.TextSearchLimit {
			result.Truncated = true
			displayed = matches[:protocol.TextSearchLimit-total]
		}
		digest := sha256.Sum256([]byte(content))
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
			end := strings.IndexByte(content[match.Start:], '\n')
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
			file.Matches = append(file.Matches, protocol.TextMatch{Start: match.Start, End: match.End, Line: line, Column: utf8.RuneCountInString(content[lineStart:match.Start]) + 1, PreviewStart: start, Preview: strings.Clone(strings.TrimSuffix(content[start:end], "\r"))})
		}
		total += len(displayed)
		budget += len(file.Path) + len(file.Digest) + 16
		for _, match := range file.Matches {
			budget += 24 + len(match.Preview)
		}
		if budget > protocol.MaxMessage-4096 {
			searchErr = fmt.Errorf("搜索结果过大，请缩小文件夹范围")
			return false
		}
		result.Files = append(result.Files, file)
		return true
	}
	err = fs.WalkDir(r.FS(), directory, func(name string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			if name == directory {
				return walkErr
			}
			result.Skipped++
			return nil
		}
		if entry.IsDir() {
			if name != directory {
				switch entry.Name() {
				case ".git", "node_modules", "dist":
					return fs.SkipDir
				}
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() || !protocol.IsTextFile(name) {
			return nil
		}
		content, opened := documents[name]
		if opened {
			delete(documents, name)
		} else {
			f, err := r.Open(name)
			if err != nil {
				result.Skipped++
				return nil
			}
			info, err := f.Stat()
			if err != nil || info.Size() > protocol.TextSearchFileLimit {
				f.Close()
				result.Skipped++
				return nil
			}
			data, err := io.ReadAll(io.LimitReader(f, protocol.TextSearchFileLimit+1))
			f.Close()
			if err != nil || len(data) > protocol.TextSearchFileLimit {
				result.Skipped++
				return nil
			}
			content = string(data)
		}
		if !add(name, content) {
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Include unsaved buffers whose files were removed on disk.
	for name, content := range documents {
		if searchErr != nil {
			break
		}
		if directory == "." || strings.HasPrefix(name, directory+"/") {
			if protocol.IsTextFile(path.Base(name)) {
				add(name, content)
			}
		}
	}
	if searchErr != nil {
		return searchErr
	}
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
	return nil
}
