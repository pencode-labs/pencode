package workbench

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/onix-engine/onix/html/parse"
	uinode "github.com/onix-engine/onix/scene/node"
	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/localization"
	"strconv"
)

type pencodeLanguagePack struct {
	pack  localization.Pack
	owner string
}

type pencodeLocalizedBinding struct{ ref, attribute, source string }

// Bind authored UI text separately from filenames, editor content and extension
// views. This runs once when the page is compiled, never during rendering.
func localizedPencodeTemplate(source, tooltipPrefix string) (*uinode.Node, []pencodeLocalizedBinding, error) {
	root, err := parse.ParseTree(source)
	if err != nil {
		return nil, nil, err
	}
	var bindings []pencodeLocalizedBinding
	localeIndex, buttonIndex := 0, 0
	refFor := func(n *uinode.Node) string {
		if ref, ok := n.Attr("data-ref"); ok {
			return ref
		}
		localeIndex++
		ref := "locale-" + strconv.Itoa(localeIndex)
		n.SetAttr("data-ref", ref)
		return ref
	}
	hasHan := func(r rune) bool { return unicode.Is(unicode.Han, r) }
	var walk func(*uinode.Node)
	walk = func(n *uinode.Node) {
		for _, name := range [...]string{"title", "aria-label", "placeholder", "alt"} {
			if value := n.AttrValue(name); strings.ContainsFunc(value, hasHan) {
				bindings = append(bindings, pencodeLocalizedBinding{refFor(n), name, value})
			}
		}
		for i, child := range n.Children {
			if child.Type == uinode.ElementNode {
				walk(child)
			} else if child.Type == uinode.TextNode && strings.ContainsFunc(child.Data, hasHan) && n.Type == uinode.ElementNode {
				target := n
				if len(n.Children) > 1 {
					target = uinode.NewElement("span")
					target.Parent, n.Children[i] = n, target
					target.AppendChild(child)
				}
				bindings = append(bindings, pencodeLocalizedBinding{refFor(target), "", strings.TrimSpace(child.Data)})
			}
		}
		// Anonymous buttons need stable IDs for native hover state.
		if n.Data == "button" && n.AttrValue("id") == "" {
			ref := n.AttrValue("data-ref")
			if ref == "" {
				ref = "button-" + strconv.Itoa(buttonIndex)
			}
			n.SetAttr("id", tooltipPrefix+ref)
			buttonIndex++
		}
	}
	walk(root)
	return root, bindings, nil
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
