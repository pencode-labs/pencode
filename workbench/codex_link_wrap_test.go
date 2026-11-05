package workbench

import (
	"os"
	"strings"
	"testing"

	"github.com/diiyw/onix/font"
	"github.com/diiyw/onix/html/css"
	"github.com/diiyw/onix/html/dom"
	"github.com/diiyw/onix/html/layout"
	"github.com/diiyw/onix/html/style"
)

func TestCodexLongLinksWrapInsideUserBubble(t *testing.T) {
	chatCSS, err := os.ReadFile("../extensions/codex/resources/chat.css")
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := css.Parse(string(chatCSS))
	if err != nil {
		t.Fatal(err)
	}
	face, err := font.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = face.Close() })
	const label = "https://paoyoudocs.feishu.cn/wiki/GKgGwCZwYj43miknfcrccOwAnzg?fromScene=spaceOverview"
	for _, width := range []float64{360, 720, 1000} {
		root, err := dom.Parse(`<article class="codex-chat"><section class="codex-message codex-user"><div class="codex-user-bubble"><p><a role="link" class="codex-link" href="` + label + `"><img class="codex-link-icon" alt="">` + label + `</a><br>根据这个看看现有功能有没有，整理一份开发文档</p></div></section></article>`)
		if err != nil {
			t.Fatal(err)
		}
		computed, err := style.ComputeWithViewport(root, sheet, style.Viewport{Width: width, Height: 600})
		if err != nil {
			t.Fatal(err)
		}
		box, err := layout.Build(root, computed, face, width, 600)
		if err != nil {
			t.Fatal(err)
		}
		var bubble *layout.Box
		var fragments []layout.TextFragment
		var collect func(*layout.Box)
		collect = func(box *layout.Box) {
			if class, _ := box.Node.Attr("class"); class == "codex-user-bubble" {
				bubble = box
			}
			for _, fragment := range box.TextFragments {
				if strings.Contains(label, fragment.Text) && fragment.Text != "" {
					fragments = append(fragments, fragment)
				}
			}
			for _, child := range box.Children {
				collect(child)
			}
		}
		collect(box)
		if bubble == nil || len(fragments) == 0 {
			t.Fatal("missing bubble or link text")
		}
		var text strings.Builder
		for _, fragment := range fragments {
			text.WriteString(fragment.Text)
			if fragment.Rect.X < bubble.Content.X-.01 || fragment.Rect.X+fragment.Rect.Width > bubble.Content.X+bubble.Content.Width+.01 || fragment.Rect.Y+fragment.Rect.Height > bubble.Content.Y+bubble.Content.Height+.01 {
				t.Errorf("viewport %g: link text %+v outside bubble content %+v", width, fragment.Rect, bubble.Content)
			}
		}
		if text.String() != label || width <= 720 && fragments[len(fragments)-1].Rect.Y <= fragments[0].Rect.Y {
			t.Errorf("viewport %g: long URL did not wrap intact: %q", width, text.String())
		}
	}
}
