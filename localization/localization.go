// Package localization loads immutable UI language packs. Message keys are the
// authored Simplified Chinese strings; missing entries fall back to English.
package localization

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

const Default = "zh-CN"

// Pack is immutable after loading and safe to share between workbench views.
type Pack struct {
	Messages map[string]string `json:"messages"`
	ID       string            `json:"id"`
	Label    string            `json:"label"`
}

//go:embed locales/*.json
var resources embed.FS

var builtins = func() []Pack {
	names, err := resources.ReadDir("locales")
	if err != nil {
		panic(err)
	}
	packs := make([]Pack, 0, len(names))
	for _, name := range names {
		data, err := resources.ReadFile("locales/" + name.Name())
		if err != nil {
			panic(err)
		}
		pack, err := Parse(data)
		if err != nil {
			panic(err)
		}
		packs = append(packs, pack)
	}
	return packs
}()

// Builtins returns a copy of the catalog; callers must not mutate message maps.
func Builtins() []Pack { return append([]Pack(nil), builtins...) }

func ValidID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for i, c := range id {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && (c == '-' || c >= '0' && c <= '9') {
			continue
		}
		return false
	}
	return true
}

func Parse(data []byte) (Pack, error) {
	var pack Pack
	if len(data) > 1<<20 || !utf8.Valid(data) {
		return pack, fmt.Errorf("invalid language pack")
	}
	if err := json.Unmarshal(data, &pack); err != nil {
		return pack, err
	}
	if !ValidID(pack.ID) || strings.TrimSpace(pack.Label) == "" || len(pack.Label) > 256 || len(pack.Messages) > 8192 {
		return Pack{}, fmt.Errorf("invalid language pack metadata")
	}
	for key, value := range pack.Messages {
		if key == "" || value == "" || len(key) > 4096 || len(value) > 16384 {
			return Pack{}, fmt.Errorf("invalid language pack message %q", key)
		}
	}
	return pack, nil
}

// English is the common fallback for incomplete or unavailable language packs.
func English() Pack {
	for _, pack := range builtins {
		if pack.ID == "en" {
			return pack
		}
	}
	return Pack{}
}

// Text performs map lookups only; it does not allocate or format user data.
func (pack Pack) Text(source string) string {
	if translated := pack.Messages[source]; translated != "" {
		return translated
	}
	if pack.ID != Default {
		if translated := English().Messages[source]; translated != "" {
			return translated
		}
	}
	return source
}
