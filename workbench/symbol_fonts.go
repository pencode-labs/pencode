package workbench

import (
	_ "embed"
	"sync"

	"github.com/diiyw/onix/font"
	"github.com/diiyw/onix/text"
)

//go:embed fonts/SymbolsNerdFontMono-Regular.ttf
var pencodeSymbolData []byte

// Shared across workspace views and font selections; regular text still uses
// the selected system or extension face and its original line metrics.
var pencodeSymbols = sync.OnceValues(func() (*font.Face, error) {
	return font.Parse(pencodeSymbolData)
})

func newPencodeTextFont(face *font.Face) *text.Font {
	symbols, _ := pencodeSymbols()
	face.UseFallback(symbols)
	return text.NewFont(face)
}
