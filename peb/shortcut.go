package peb

import "strings"

// Shortcut is the parsed, platform-independent form of an Action.Shortcut.
// Primary means Command on macOS and Control elsewhere.
type Shortcut struct {
	Key                                 string
	Primary, Control, Super, Shift, Alt bool
}

// ParseShortcut accepts a single chord with case-sensitive key names.
// Successful parsing does not allocate.
func ParseShortcut(chord string) (Shortcut, error) {
	var s Shortcut
	for part := range strings.SplitSeq(chord, "+") {
		if s.Key != "" {
			return Shortcut{}, ErrProtocol
		}
		var modifier *bool
		switch part {
		case "Primary":
			modifier = &s.Primary
		case "Ctrl":
			modifier = &s.Control
		case "Super":
			modifier = &s.Super
		case "Shift":
			modifier = &s.Shift
		case "Alt":
			modifier = &s.Alt
		default:
			if !validShortcutKey(part) {
				return Shortcut{}, ErrProtocol
			}
			s.Key = part
		}
		if modifier != nil {
			if *modifier {
				return Shortcut{}, ErrProtocol
			}
			*modifier = true
		}
	}
	if s.Key == "" {
		return Shortcut{}, ErrProtocol
	}
	return s, nil
}

func validShortcutKey(key string) bool {
	if len(key) == 1 {
		c := key[0]
		return c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("=-,./;[]\\'`", rune(c))
	}
	switch key {
	case "F1", "F2", "F3", "F4", "F5", "F6", "F7", "F8", "F9", "F10", "F11", "F12",
		"Escape", "Tab", "Enter", "Space", "Backspace", "Delete", "Insert", "Home", "End", "PageUp", "PageDown", "Left", "Right", "Up", "Down":
		return true
	}
	return false
}
