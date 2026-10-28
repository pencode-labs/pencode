// Package textchange computes UTF-8 edit ranges shared by protocol consumers.
package textchange

import "unicode/utf8"

// Between returns one replacement [start, oldEnd) -> next[start:newEnd].
// Compare blocks before individual bytes so late edits do not decode the entire
// common prefix. Keep CRLF and UTF-8 characters atomic at both endpoints.
func Between(old, next string) (start, oldEnd, newEnd int) {
	const block = 256
	limit := min(len(old), len(next))
	for start+block <= limit && old[start:start+block] == next[start:start+block] {
		start += block
	}
	for start < limit && old[start] == next[start] {
		start++
	}
	for start > 0 && (start < len(old) && !utf8.RuneStart(old[start]) || start < len(next) && !utf8.RuneStart(next[start])) {
		start--
	}
	if start > 0 && (start < len(old) && old[start-1] == '\r' && old[start] == '\n' || start < len(next) && next[start-1] == '\r' && next[start] == '\n') {
		start--
	}
	oldEnd, newEnd = len(old), len(next)
	for oldEnd-start >= block && newEnd-start >= block && old[oldEnd-block:oldEnd] == next[newEnd-block:newEnd] {
		oldEnd -= block
		newEnd -= block
	}
	for oldEnd > start && newEnd > start && old[oldEnd-1] == next[newEnd-1] {
		oldEnd--
		newEnd--
	}
	for oldEnd < len(old) && !utf8.RuneStart(old[oldEnd]) {
		oldEnd++
		newEnd++
	}
	if oldEnd > 0 && oldEnd < len(old) && old[oldEnd-1] == '\r' && old[oldEnd] == '\n' || newEnd > 0 && newEnd < len(next) && next[newEnd-1] == '\r' && next[newEnd] == '\n' {
		oldEnd++
		newEnd++
	}
	return
}
