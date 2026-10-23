package workbench

import "strings"

type sourceConflict struct {
	Start, End      int
	Local, Incoming string
}

// Byte offsets preserve CRLF, trailing newlines and all text outside a conflict.
func sourceConflicts(value string) []sourceConflict {
	var conflicts []sourceConflict
	start, local, base, incoming, offset := -1, -1, -1, -1, 0
	for line := range strings.SplitAfterSeq(value, "\n") {
		text := strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(text, "<<<<<<< ") && start < 0:
			start, local, base, incoming = offset, offset+len(line), -1, -1
		case strings.HasPrefix(text, "||||||| ") && start >= 0 && incoming < 0:
			base = offset
		case text == "=======" && start >= 0 && incoming < 0:
			if base < 0 {
				base = offset
			}
			incoming = offset + len(line)
		case strings.HasPrefix(text, ">>>>>>> ") && start >= 0 && incoming >= 0:
			conflicts = append(conflicts, sourceConflict{Start: start, End: offset + len(line), Local: value[local:base], Incoming: value[incoming:offset]})
			start = -1
		}
		offset += len(line)
	}
	return conflicts
}

func resolveSourceConflict(value string, index int, side string) string {
	conflicts := sourceConflicts(value)
	if index < 0 || index >= len(conflicts) {
		return value
	}
	c := conflicts[index]
	replacement := ""
	switch side {
	case "local":
		replacement = c.Local
	case "incoming":
		replacement = c.Incoming
	case "both":
		replacement = c.Local + c.Incoming
	default:
		return value
	}
	return value[:c.Start] + replacement + value[c.End:]
}

// Comparison and ordinary files share delta history and the same memory budget.
func (t *sourceTab) editResult(value string) {
	if value == t.Result {
		return
	}
	start, oldEnd, newEnd, _, changed := t.buffer.ChangedRange(t.Result, value)
	if !changed {
		start, oldEnd, newEnd, _ = pencodeChangedRange(t.Result, value)
	}
	t.recordEdit(pencodeEdit{start: start, removed: t.Result[start:oldEnd], inserted: value[start:newEnd]})
	t.replaceContent(t.Result, value, start, oldEnd, newEnd)
	t.Result = value
}
func (t *sourceTab) undoResult(redo bool) bool {
	value, _, ok := t.restoreEdit(t.Result, redo)
	if ok {
		t.Result = value
	}
	return ok
}
