package extensions

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/diiyw/onix/font"
	"github.com/diiyw/onix/images/svg"
	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/output"
	"github.com/pencode-labs/pencode/peb"
	"github.com/diiyw/onix/syntax"
)

const (
	maxResourceBytes = 64 << 20
	maxFontBytes     = 32 << 20
)

// Font holds parsed, memory-backed faces validated on the loader worker.
type Font struct {
	ID, Label     string
	Regular, Bold *font.Face
}

type Theme struct {
	ID, Label string
	Theme     *syntax.Theme
}

// Bundle owns all imported assets. No extension directory remains open after
// registration, and rendering performs no disk reads for extension icons.
type Bundle struct {
	Actions         *ActionExtension
	Themes          []Theme
	IconThemes      []peb.IconTheme
	Icons           map[string][]byte
	Editors         []Editor
	Fonts           []Font
	LanguageServers []LanguageServer
}

// Close releases an unconsumed registration. Once imported into a workbench,
// its font registry owns these faces instead.
func (b *Bundle) Close() {
	if b == nil {
		return
	}
	closed := make(map[*font.Face]bool, len(b.Fonts)*2)
	for _, item := range b.Fonts {
		for _, face := range [...]*font.Face{item.Regular, item.Bold} {
			if face != nil && !closed[face] {
				face.Close()
				closed[face] = true
			}
		}
	}
	b.Fonts = nil
}

// Editor retains its validated process entry for on-demand activation.
type Editor struct {
	Output *output.Channel
	peb.Editor
	LocalID string
	remote  *client.Extension
}

func validResourcePath(name string, resource bool) bool {
	return fs.ValidPath(name) && name != "." && !strings.ContainsAny(name, "\\:?#\x00") &&
		(!resource || strings.HasPrefix(name, "resources/"))
}

type resourceFiles struct {
	source fs.FS
	cache  map[string][]byte
	bytes  int
}

func (f *resourceFiles) Open(name string) (fs.File, error) {
	if !validResourcePath(name, true) {
		return nil, fs.ErrInvalid
	}
	return f.source.Open(name)
}

func (f *resourceFiles) ReadFile(name string) ([]byte, error) {
	return f.readFile(name, 1<<20)
}

func (f *resourceFiles) readFile(name string, limit int64) ([]byte, error) {
	if !validResourcePath(name, true) {
		return nil, fmt.Errorf("invalid resource path %q", name)
	}
	if data, ok := f.cache[name]; ok {
		if int64(len(data)) > limit {
			return nil, fmt.Errorf("oversized resource %q", name)
		}
		return data, nil
	}
	data, err := readResource(f.source, name, min(limit, int64(maxResourceBytes-f.bytes)))
	if err != nil {
		return nil, err
	}
	f.bytes += len(data)
	f.cache[name] = data
	return data, nil
}

func loadBundle(source fs.FS, id string, c peb.Contributions) (result *Bundle, failure error) {
	servers, err := loadLanguageServers(id, c.LanguageServers)
	if err != nil {
		return nil, err
	}
	files := resourceFiles{source: source, cache: make(map[string][]byte)}
	bundle := &Bundle{Icons: make(map[string][]byte), LanguageServers: servers}
	if len(c.Actions) > maxActions {
		return nil, fmt.Errorf("too many extension actions")
	}
	if c.ActionService || len(c.Actions) > 0 {
		bundle.Actions = &ActionExtension{ID: id, Actions: make([]peb.Action, 0, len(c.Actions))}
		seen := make(map[string]bool, len(c.Actions))
		for _, a := range c.Actions {
			if seen[a.ID] {
				return nil, fmt.Errorf("duplicate action %q", a.ID)
			}
			seen[a.ID] = true
			loaded, err := loadAction(&files, id, a, bundle.Icons)
			if err != nil {
				return nil, err
			}
			bundle.Actions.Actions = append(bundle.Actions.Actions, loaded)
		}
	}
	ids := make(map[string]bool)
	faces := make(map[string]*font.Face)
	defer func() {
		if failure != nil {
			for _, face := range faces {
				face.Close()
			}
		}
	}()
	loadFace := func(name string) (*font.Face, error) {
		if !validResourcePath(name, true) || (path.Ext(name) != ".ttf" && path.Ext(name) != ".otf") {
			return nil, fmt.Errorf("invalid font resource %q", name)
		}
		if face := faces[name]; face != nil {
			return face, nil
		}
		data, err := files.readFile(name, maxFontBytes)
		if err != nil {
			return nil, err
		}
		face, err := font.Parse(data)
		if err != nil {
			return nil, fmt.Errorf("font %s: %w", name, err)
		}
		faces[name] = face
		return face, nil
	}
	for _, f := range c.Fonts {
		if !validID(f.ID) || f.Label == "" || ids["font/"+f.ID] {
			return nil, fmt.Errorf("invalid or duplicate font id %q", f.ID)
		}
		ids["font/"+f.ID] = true
		regular, err := loadFace(f.Path)
		if err != nil {
			return nil, err
		}
		var bold *font.Face
		if f.BoldPath != "" {
			bold, err = loadFace(f.BoldPath)
			if err != nil {
				return nil, err
			}
		}
		bundle.Fonts = append(bundle.Fonts, Font{"extensions/" + id + "/" + f.ID, f.Label, regular, bold})
	}
	for _, editor := range c.Editors {
		if !validID(editor.ID) || editor.Label == "" || ids["editor/"+editor.ID] || len(editor.Selectors) == 0 {
			return nil, fmt.Errorf("invalid or duplicate editor id %q", editor.ID)
		}
		ids["editor/"+editor.ID] = true
		for _, rule := range editor.Selectors {
			if rule.Match != peb.FileName && rule.Match != peb.FileSuffix || !validRule(peb.IconRule{Match: rule.Match, Pattern: rule.Pattern}) {
				return nil, fmt.Errorf("invalid editor selector %q", rule.Pattern)
			}
		}
		localID := editor.ID
		editor.ID = "extensions/" + id + "/" + localID
		bundle.Editors = append(bundle.Editors, Editor{Editor: editor, LocalID: localID})
	}
	for _, t := range c.Themes {
		if !validID(t.ID) || t.Label == "" || ids["theme/"+t.ID] {
			return nil, fmt.Errorf("invalid or duplicate theme id %q", t.ID)
		}
		ids["theme/"+t.ID] = true
		if ext := path.Ext(t.Path); !validResourcePath(t.Path, true) || ext != ".json" && ext != ".jsonc" {
			return nil, fmt.Errorf("invalid theme resource %q", t.Path)
		}
		theme, err := syntax.LoadTheme(&files, t.Path)
		if err != nil {
			return nil, err
		}
		bundle.Themes = append(bundle.Themes, Theme{"extensions/" + id + "/" + t.ID, t.Label, theme})
	}
	for _, t := range c.IconThemes {
		if !validID(t.ID) || t.Label == "" || ids["icons/"+t.ID] {
			return nil, fmt.Errorf("invalid or duplicate icon theme id %q", t.ID)
		}
		ids["icons/"+t.ID] = true
		t.ID = "extensions/" + id + "/" + t.ID
		for i := range t.Rules {
			r := &t.Rules[i]
			if !validRule(*r) {
				return nil, fmt.Errorf("invalid icon rule %q", r.Pattern)
			}
			if path.Ext(r.Path) != ".svg" {
				return nil, fmt.Errorf("icon must be an SVG: %q", r.Path)
			}
			data, err := files.ReadFile(r.Path)
			if err != nil {
				return nil, err
			}
			source := "peb://" + id + "/" + r.Path
			if _, ok := bundle.Icons[source]; !ok {
				config, err := svg.DecodeConfig(bytes.NewReader(data))
				if err != nil {
					return nil, fmt.Errorf("icon %s: %w", r.Path, err)
				}
				if config.Width < 1 || config.Height < 1 || config.Width > 256 || config.Height > 256 {
					return nil, fmt.Errorf("icon dimensions must be 1..256: %s", r.Path)
				}
				bundle.Icons[source] = data
			}
			r.Path = source
		}
		bundle.IconThemes = append(bundle.IconThemes, t)
	}
	return bundle, nil
}

func contributionResources(c peb.Contributions) []string {
	seen := make(map[string]struct{})
	add := func(name string) {
		if validResourcePath(name, true) {
			seen[name] = struct{}{}
		}
	}
	for _, action := range c.Actions {
		add(action.Icon)
	}
	for _, theme := range c.Themes {
		add(theme.Path)
	}
	for _, theme := range c.IconThemes {
		for _, rule := range theme.Rules {
			add(rule.Path)
		}
	}
	for _, face := range c.Fonts {
		add(face.Path)
		add(face.BoldPath)
	}
	resources := make([]string, 0, len(seen))
	for name := range seen {
		resources = append(resources, name)
	}
	sort.Strings(resources)
	return resources
}

func validRule(r peb.IconRule) bool {
	if strings.ContainsAny(r.Pattern, "/\\\x00") {
		return false
	}
	switch r.Match {
	case peb.DefaultFile, peb.DefaultFolder:
		return r.Pattern == ""
	case peb.FileName, peb.FolderName:
		return r.Pattern != "" && r.Pattern != "." && r.Pattern != ".."
	case peb.FileSuffix:
		return len(r.Pattern) > 1 && r.Pattern[0] == '.'
	}
	return false
}
