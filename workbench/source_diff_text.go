package workbench

import "strings"

// Keep the original text, including line endings, and reuse nodes on repaint.
func sourceDiffText(b *sourceVNodeView, text string) {
	inHunk, offset := false, 0
	for line := range strings.SplitAfterSeq(text, "\n") {
		class := ""
		switch {
		case strings.HasPrefix(line, "diff "):
			class, inHunk = "sc-diff-file", false
		case strings.HasPrefix(line, "@@"):
			class, inHunk = "sc-diff-hunk", true
		case !inHunk && (strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ")):
			class = "sc-diff-file"
		case strings.HasPrefix(line, "+"):
			class = "sc-diff-added"
		case strings.HasPrefix(line, "-"):
			class = "sc-diff-deleted"
		case strings.HasPrefix(line, "index "), strings.HasPrefix(line, "new file mode "),
			strings.HasPrefix(line, "deleted file mode "), strings.HasPrefix(line, "old mode "),
			strings.HasPrefix(line, "new mode "), strings.HasPrefix(line, "similarity index "),
			strings.HasPrefix(line, "rename from "), strings.HasPrefix(line, "rename to "),
			strings.HasPrefix(line, "\\ No newline at end of file"):
			class = "sc-diff-meta"
		}
		// Byte offsets give repeated lines distinct stable keys without formatting.
		b.openKey("span", uint64(offset+1), "class", class)
		b.content(line)
		b.close()
		offset += len(line)
	}
}
