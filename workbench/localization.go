package workbench

import (
	"fmt"
	"strings"
	"unicode"

	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/localization"
	nethtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

type pencodeLanguagePack struct {
	pack  localization.Pack
	owner string
}

type pencodeLocalizedBinding struct{ ref, attribute, source string }

// Bind authored UI text separately from filenames, editor content and extension
// views. This runs once when the page is compiled, never during rendering.
func localizedPencodeTemplate(source string) (string, []pencodeLocalizedBinding, error) {
	nodes, err := nethtml.ParseFragment(strings.NewReader(source), &nethtml.Node{Type: nethtml.ElementNode, Data: "div", DataAtom: atom.Div})
	if err != nil {
		return "", nil, err
	}
	var bindings []pencodeLocalizedBinding
	index := 0
	refFor := func(node *nethtml.Node) string {
		for _, attr := range node.Attr {
			if attr.Key == "data-ref" {
				return attr.Val
			}
		}
		index++
		ref := fmt.Sprintf("locale-%d", index)
		node.Attr = append(node.Attr, nethtml.Attribute{Key: "data-ref", Val: ref})
		return ref
	}
	var walk func(*nethtml.Node)
	walk = func(node *nethtml.Node) {
		if node.Type != nethtml.ElementNode {
			return
		}
		for _, attr := range node.Attr {
			switch attr.Key {
			case "title", "aria-label", "placeholder", "alt":
				if strings.ContainsFunc(attr.Val, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
					bindings = append(bindings, pencodeLocalizedBinding{refFor(node), attr.Key, attr.Val})
				}
			}
		}
		for child := node.FirstChild; child != nil; {
			next := child.NextSibling
			if child.Type == nethtml.TextNode && strings.ContainsFunc(child.Data, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
				target := node
				if node.FirstChild != node.LastChild {
					target = &nethtml.Node{Type: nethtml.ElementNode, Data: "span", DataAtom: atom.Span}
					node.InsertBefore(target, child)
					node.RemoveChild(child)
					target.AppendChild(child)
				}
				bindings = append(bindings, pencodeLocalizedBinding{refFor(target), "", strings.TrimSpace(child.Data)})
			} else {
				walk(child)
			}
			child = next
		}
	}
	var out strings.Builder
	for _, node := range nodes {
		walk(node)
		if err := nethtml.Render(&out, node); err != nil {
			return "", nil, err
		}
	}
	return out.String(), bindings, nil
}

func (m *workspaceModel) ensureLanguagePacks() {
	m.ensureSettings()
	if m.languagePacks != nil {
		return
	}
	for _, pack := range localization.Builtins() {
		m.languagePacks = append(m.languagePacks, pencodeLanguagePack{pack: pack})
	}
	m.selectLanguage()
}

func (m *workspaceModel) tr(source string) string {
	if m.locale.ID == "" {
		return source
	}
	return m.locale.Text(source)
}

func (m *workspaceModel) selectLanguage() {
	selected := m.settings.Language
	m.locale = localization.English()
	for _, entry := range m.languagePacks {
		if entry.pack.ID == selected {
			m.locale = entry.pack
			break
		}
	}
	if m.lineInfo != "" {
		m.lineInfo = fmt.Sprintf(m.tr("行 %d · %d 字符"), m.lineCount, m.characterCount)
	}
	m.languageOptions = m.languageOptions[:0]
	found := false
	for _, entry := range m.languagePacks {
		active := entry.pack.ID == selected
		found = found || active
		m.languageOptions = append(m.languageOptions, appearanceOption(componentKey(entry.pack.ID), entry.pack.ID, entry.pack.Label, active))
	}
	// Keep an unavailable plugin selection while its registration is loading.
	if !found && selected != "" {
		m.languageOptions = append(m.languageOptions, appearanceOption(componentKey(selected), selected, selected, true))
	}
}

func (m *workspaceModel) changeLanguage(id string) bool {
	m.ensureLanguagePacks()
	for _, entry := range m.languagePacks {
		if entry.pack.ID != id {
			continue
		}
		if m.settings.Language == id {
			return true
		}
		m.settings.Language = id
		m.selectLanguage()
		m.refreshSettingsLabels()
		m.rebuildIconThemeOptions()
		m.fullRefresh = true
		m.tooltip = pencodeTooltip{}
		m.saveSettings()
		return true
	}
	return false
}

func (m *workspaceModel) importLanguagePacks(owner string, packs []localization.Pack) {
	if len(packs) == 0 {
		return
	}
	m.ensureLanguagePacks()
	for _, pack := range packs {
		duplicate := false
		for _, entry := range m.languagePacks {
			if entry.pack.ID == pack.ID {
				duplicate = true
				break
			}
		}
		if !duplicate {
			m.languagePacks = append(m.languagePacks, pencodeLanguagePack{pack: pack, owner: owner})
		}
	}
	m.selectLanguage()
	m.refreshSettingsLabels()
	m.fullRefresh = true
}

func (m *workspaceModel) removeLanguagePacks(prefix string) {
	if len(m.languagePacks) == 0 {
		return
	}
	packs := m.languagePacks[:0]
	for _, entry := range m.languagePacks {
		if entry.owner != "" && strings.HasPrefix("extensions/"+entry.owner+"/", prefix) {
			continue
		}
		packs = append(packs, entry)
	}
	if len(packs) == len(m.languagePacks) {
		return
	}
	clear(m.languagePacks[len(packs):])
	m.languagePacks = packs
	m.selectLanguage()
	m.refreshSettingsLabels()
	m.fullRefresh = true
}

func renderPencodeTranslations(m *workspaceModel, view *htmlui.View, bindings []pencodeLocalizedBinding) {
	m.ensureLanguagePacks()
	for _, binding := range bindings {
		text := m.tr(binding.source)
		if binding.attribute == "" {
			view.Text(binding.ref, text)
		} else {
			view.Attr(binding.ref, binding.attribute, text)
		}
	}
	view.Attr("root", "lang", m.locale.ID)
	view.Keyed("language-select", m.languageOptions)
	view.Event("language-select", htmlui.EventChange)
}
