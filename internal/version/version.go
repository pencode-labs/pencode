// Package version identifies the standalone server build and its release assets.
package version

import "strings"

// Value is set by release builds with -X.
var Value = "dev"

const Symbol = "github.com/pencode-labs/pencode/internal/version.Value"

func Valid(value string) bool {
	return value != "" && strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(".-_", r))
	}) < 0
}
