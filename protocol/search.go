package protocol

import "github.com/pencode-labs/pencode/textsearch"

const TextSearchLimit = 2000
const TextSearchFileLimit = 2 << 20
const TextSearchFilesLimit = 65536

type SearchDocument struct{ Path, Content string }
type TextMatch struct {
	Start, End, Line, Column int
	PreviewStart             int
	Preview                  string
}
type SearchFile struct {
	Path, Digest string
	Count        int
	Matches      []TextMatch
}
type SearchResult struct {
	Files     []SearchFile
	Truncated bool
	Skipped   int
}

func SearchFlags(options textsearch.Options) uint32 {
	var flags uint32
	if options.CaseSensitive {
		flags |= 1
	}
	if options.WholeWord {
		flags |= 2
	}
	if options.Regex {
		flags |= 4
	}
	if options.PreserveCase {
		flags |= 8
	}
	return flags
}
func SearchOptions(flags uint32) textsearch.Options {
	return textsearch.Options{CaseSensitive: flags&1 != 0, WholeWord: flags&2 != 0, Regex: flags&4 != 0, PreserveCase: flags&8 != 0}
}
