package workbench

import (
	"strings"

	"github.com/onix-engine/onix/html/parse"
	uinode "github.com/onix-engine/onix/scene/node"
	htmlui "github.com/onix-engine/onix/ui/html"
)

type pencodeExtensionExpanded struct{ ref, value string }

type pencodeExtensionMarkup struct {
	tree     *uinode.Node
	template htmlui.Template
	html     string
	expanded []pencodeExtensionExpanded
}

// Keep expansion in render state so a menu toggle preserves a pressed button.
// The wire source is also the fast path for unchanged transcript regions.
func extensionMarkup(source string, old *pencodeExtensionMarkup, detach bool) (pencodeExtensionMarkup, error) {
	if old != nil && old.tree != nil && old.html == source {
		return *old, nil
	}
	// Parsed names and text may borrow the input. A small, long-lived region
	// must not retain a whole obsolete view after the other regions change.
	if detach {
		source = strings.Clone(source)
	}
	tree, err := parse.ParseTree(source)
	if err != nil {
		return pencodeExtensionMarkup{}, err
	}
	next := pencodeExtensionMarkup{tree: tree, html: source}
	var prior *uinode.Node
	if old != nil {
		prior = old.tree
	}
	if next.normalize(tree, prior) {
		next.tree, next.template = old.tree, old.template
		return next, nil
	}
	// Template compilation consumes metadata such as embedded styles. Keep the
	// authored tree for validation, selection and subsequent structural comparisons.
	var arena uinode.Arena
	next.template, err = htmlui.NewDocumentTemplate(arena.Clone(tree))
	return next, err
}

func (m *pencodeExtensionMarkup) normalize(n, previous *uinode.Node) bool {
	if value, ok := n.Attr("aria-expanded"); ok {
		if ref := n.AttrValue("data-ref"); ref != "" {
			m.expanded = append(m.expanded, pencodeExtensionExpanded{ref, value})
			n.SetAttr("aria-expanded", "")
		}
	}
	equal := previous != nil && n.Type == previous.Type && n.Namespace == previous.Namespace && n.Data == previous.Data && len(n.Children) == len(previous.Children) && n.EqualAttributes(previous)
	for i, child := range n.Children {
		var prior *uinode.Node
		if equal {
			prior = previous.Children[i]
		}
		equal = m.normalize(child, prior) && equal
	}
	return equal
}
