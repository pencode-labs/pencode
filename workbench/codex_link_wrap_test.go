package workbench

import (
	"math"
	"os"
	"strings"
	"testing"

	"github.com/onix-engine/onix/font"
	"github.com/onix-engine/onix/html/css"
	"github.com/onix-engine/onix/html/dom"
	"github.com/onix-engine/onix/html/layout"
	"github.com/onix-engine/onix/html/style"
)

func TestCodexLinkIconsAlignWithText(t *testing.T) {
	chatCSS, err := os.ReadFile("../extensions/codex/resources/chat.css")
	if err != nil {
		t.Fatal(err)
	}
	face, err := font.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = face.Close() })
	for _, typography := range []string{"12px/1.2 ui", "14px/1.7 ui", "18px/2 monospace"} {
		for _, label := range []string{"Dockerfile", "<code>Dockerfile</code>"} {
			t.Run(typography+"/"+label, func(t *testing.T) {
				root, err := dom.Parse(`<article class="codex-chat"><p><a class="codex-link"><img class="codex-link-icon" src="icons/code.svg">` + label + `</a> 在生产使用</p></article>`)
				if err != nil {
					t.Fatal(err)
				}
				sheet, err := css.Parse(string(chatCSS) + `.codex-chat { font: ` + typography + `; }`)
				if err != nil {
					t.Fatal(err)
				}
				computed, err := style.Compute(root, sheet)
				if err != nil {
					t.Fatal(err)
				}
				box, err := layout.Build(root, computed, face, 720, 600)
				if err != nil {
					t.Fatal(err)
				}
				var icon *layout.ImageFragment
				var text *layout.TextFragment
				var collect func(*layout.Box)
				collect = func(box *layout.Box) {
					for i := range box.ImageFragments {
						icon = &box.ImageFragments[i]
					}
					for i := range box.TextFragments {
						if box.TextFragments[i].Text == "Dockerfile" {
							text = &box.TextFragments[i]
						}
					}
					for _, child := range box.Children {
						collect(child)
					}
				}
				collect(box)
				if icon == nil || text == nil || len(text.Glyphs) == 0 {
					t.Fatal("missing link icon or label")
				}
				parent, _ := computed.Get(icon.Node.Parent)
				xHeight := float64(face.XHeight()) * parent.FontSize.LengthPx / float64(face.Head.UnitsPerEm)
				if got, want := icon.Rect.Y+icon.Rect.Height/2, text.Glyphs[0].Y-xHeight/2; math.Abs(got-want) > .01 {
					t.Errorf("icon center = %g, want %g alongside text", got, want)
				}
				if gap := text.Rect.X - icon.Rect.X - icon.Rect.Width; math.Abs(gap-4) > .01 {
					t.Errorf("icon-to-label gap = %g, want 4", gap)
				}
			})
		}
	}
}

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
		root, err := dom.Parse(`<article class="codex-chat"><section class="codex-message codex-user"><div class="codex-user-bubble"><p><a role="link" class="codex-link" href="` + label + `"><img class="codex-link-icon" src="icons/globe.svg" alt="">` + label + `</a><br>根据这个看看现有功能有没有，整理一份开发文档</p></div></section></article>`)
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
