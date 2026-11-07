package workbench

import (
	"testing"

	"github.com/onix-engine/onix/font"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/terminal"
)

func TestPencodePromptSymbolFallback(t *testing.T) {
	face, err := font.Load("../../onix/testdata/fonts/Noto_Sans_SC/static/NotoSansSC-Regular.ttf")
	if err != nil {
		t.Fatal(err)
	}
	selected := newPencodeTextFont(face)
	t.Cleanup(func() { _ = selected.Dispose() })
	symbols, err := pencodeSymbols()
	if err != nil {
		t.Fatal(err)
	}
	if owner, id := selected.ResolveGlyph('A'); owner != face || id != face.GlyphIndex('A') {
		t.Fatal("symbol fallback replaced the selected text font")
	}
	cache := selected.GlyphCache(14, 72)
	const icons = "\ue73c\ue63c\ue0b0\uf126\U000f10fe"
	for _, r := range icons {
		owner, id := selected.ResolveGlyph(r)
		if face.GlyphIndex(r) != 0 || owner != symbols || id == 0 {
			t.Fatalf("prompt icon %U did not resolve to the bundled symbols", r)
		}
		metrics := cache.GlyphMetrics(r)
		gray, lcd := cache.Glyph(r), cache.GlyphLCDAt(r, 0)
		if !gray.Visible() || !lcd.Visible() || gray.Advance != metrics.Advance || gray.Advance != lcd.Advance {
			t.Fatalf("prompt icon %U has inconsistent measurement or missing ink", r)
		}
	}
	if allocations := testing.AllocsPerRun(100, func() {
		for _, r := range icons {
			selected.ResolveGlyph(r)
			cache.GlyphMetrics(r)
			cache.Glyph(r)
			cache.GlyphLCDAt(r, 0)
		}
	}); allocations != 0 {
		t.Fatalf("cached prompt icons allocated %g times", allocations)
	}
	item := &pencodeTerminal{screen: terminal.NewScreen(16, 2)}
	model := workspaceModel{terminals: &pencodeTerminals{active: item}}
	item.screen.Feed([]byte("\x1b[36m\ue73c base\x1b[0m"))
	model.prepareTerminalView()
	if []rune(model.terminalView.value)[0] != '\ue73c' || model.terminalView.spans[0].End != len("\ue73c base") {
		t.Fatal("terminal lost the prompt icon or its color span")
	}
}

func TestPencodeExtensionFontSharesSymbolFallback(t *testing.T) {
	face, err := font.Load("../../onix/testdata/fonts/Noto_Sans_SC/static/NotoSansSC-Regular.ttf")
	if err != nil {
		t.Fatal(err)
	}
	model := workspaceModel{}
	model.importFonts([]extensions.Font{{ID: "extensions/test/font", Label: "Test", Regular: face, Bold: face}})
	model.settings.CodeFont = "extensions/test/font"
	symbols, _ := pencodeSymbols()
	selected := model.codeFont()
	if owner, id := selected.ResolveGlyph('\ue73c'); owner != symbols || id == 0 {
		t.Fatal("extension font lost the prompt symbol fallback")
	}
	model.disposeFonts()
	if symbols.GlyphIndex('\ue73c') == 0 {
		t.Fatal("disposing a selected font closed the shared symbols")
	}
}
