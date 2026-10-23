package workbench

import "unicode/utf8"

// Compare common text in blocks before decoding Unicode. Only the prefix's
// rune count is needed for carets; unchanged suffixes need no decoding.
func pencodeChangedRange(oldContent, content string) (prefix, oldSuffix, newSuffix, prefixRunes int) {
	limit := min(len(oldContent), len(content))
	const block = 64
	for prefix+block <= limit && oldContent[prefix:prefix+block] == content[prefix:prefix+block] {
		prefix += block
	}
	for prefix < limit && oldContent[prefix] == content[prefix] {
		prefix++
	}
	for prefix > 0 && (prefix < len(oldContent) && !utf8.RuneStart(oldContent[prefix]) || prefix < len(content) && !utf8.RuneStart(content[prefix])) {
		prefix--
	}
	oldSuffix, newSuffix = len(oldContent), len(content)
	for oldSuffix-prefix >= block && newSuffix-prefix >= block && oldContent[oldSuffix-block:oldSuffix] == content[newSuffix-block:newSuffix] {
		oldSuffix -= block
		newSuffix -= block
	}
	for oldSuffix > prefix && newSuffix > prefix && oldContent[oldSuffix-1] == content[newSuffix-1] {
		oldSuffix--
		newSuffix--
	}
	for oldSuffix < len(oldContent) && !utf8.RuneStart(oldContent[oldSuffix]) {
		oldSuffix++
		newSuffix++
	}
	return prefix, oldSuffix, newSuffix, utf8.RuneCountInString(oldContent[:prefix])
}
