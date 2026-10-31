package textsearch

import (
	"bytes"
	"strings"
	"unicode/utf8"
)

func index[T string | []byte](text T, query string) int {
	switch value := any(text).(type) {
	case string:
		return strings.Index(value, query)
	case []byte:
		return bytes.Index(value, []byte(query))
	}
	return -1
}

func decodeRune[T string | []byte](text T) (rune, int) {
	switch value := any(text).(type) {
	case string:
		return utf8.DecodeRuneInString(value)
	case []byte:
		return utf8.DecodeRune(value)
	}
	return utf8.RuneError, 0
}

func decodeLastRune[T string | []byte](text T) (rune, int) {
	switch value := any(text).(type) {
	case string:
		return utf8.DecodeLastRuneInString(value)
	case []byte:
		return utf8.DecodeLastRune(value)
	}
	return utf8.RuneError, 0
}
