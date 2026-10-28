package workbench

import (
	"bytes"
	"io"
	"strings"

	"golang.org/x/net/html"
)

type pencodeExtensionExpanded struct {
	ref, value string
}

// Expansion is rendered as an attribute update so opening or dismissing a
// menu does not replace a button that may already own a pointer press.
func splitExtensionExpanded(source string) (string, []pencodeExtensionExpanded, error) {
	if !strings.Contains(source, "aria-expanded") {
		return source, nil, nil
	}
	var template strings.Builder
	var expanded []pencodeExtensionExpanded
	tokens := html.NewTokenizer(strings.NewReader(source))
	offset, copied := 0, 0
	for {
		kind := tokens.Next()
		position := offset
		offset += len(tokens.Raw())
		if kind == html.ErrorToken {
			if tokens.Err() != io.EOF {
				return "", nil, tokens.Err()
			}
			break
		}
		if kind != html.StartTagToken && kind != html.SelfClosingTagToken {
			continue
		}
		if !bytes.Contains(tokens.Raw(), []byte("aria-expanded")) {
			continue
		}
		token := tokens.Token()
		ref, index := "", -1
		for i, attr := range token.Attr {
			switch attr.Key {
			case "data-ref":
				ref = attr.Val
			case "aria-expanded":
				index = i
			}
		}
		if ref == "" || index < 0 {
			continue
		}
		expanded = append(expanded, pencodeExtensionExpanded{ref: ref, value: token.Attr[index].Val})
		token.Attr[index].Val = ""
		template.WriteString(source[copied:position])
		template.WriteString(token.String())
		copied = offset
	}
	if len(expanded) == 0 {
		return source, nil, nil
	}
	template.WriteString(source[copied:])
	return template.String(), expanded, nil
}
