package workbench

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/diiyw/onix/scene"
	onixtext "github.com/diiyw/onix/text"
)

var perfTab *sourceTab
var perfView sourceDiffView
var perfRange [4]int

func perfText(b testing.TB, size int) string {
	b.Helper()
	if dir := os.Getenv("ONIX_PERF_FIXTURE"); dir != "" {
		data, err := os.ReadFile(dir + "/" + strconv.Itoa(size) + ".txt")
		if err != nil {
			b.Fatal(err)
		}
		if len(data) != size {
			b.Fatalf("plaintext length %d != %d", len(data), size)
		}
		return string(data)
	}
	return strings.Repeat("let value = 42; // 中文🙂\n", size/len("let value = 42; // 中文🙂\n")+1)[:size]
}

func BenchmarkPerfChangedRange(b *testing.B) {
	for _, size := range []int{64 << 10, 512 << 10, 2 << 20} {
		base := perfText(b, size)
		for _, pos := range []int{0, size / 2, size} {
			value := base[:pos] + "x" + base[pos:]
			b.Run(fmt.Sprintf("%d/%d", size, pos), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					a, c, d, e := pencodeChangedRange(base, value)
					perfRange = [4]int{a, c, d, e}
				}
			})
		}
	}
}

func BenchmarkPerfSourceAlign(b *testing.B) {
	for _, lines := range []int{1000, 20000, 100000} {
		base := strings.Repeat("let x = 42;\n", lines)
		if dir := os.Getenv("ONIX_PERF_FIXTURE"); dir != "" {
			data, err := os.ReadFile(dir + "/lines-" + strconv.Itoa(lines) + ".txt")
			if err != nil {
				b.Fatal(err)
			}
			base = string(data)
		}
		pos := len(base) / 2
		value := base[:pos] + "changed\n" + base[pos:]
		patch := fmt.Sprintf("@@ -%d,0 +%d @@\n", lines/2, lines/2+1)
		b.Run(strconv.Itoa(lines), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				perfView = alignSourceDiff(base, value, patch)
			}
		})
	}
}

func TestPerfSourceHistoryRetention(t *testing.T) {
	b := perfText(t, 512<<10)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	tab := &sourceTab{Kind: "changes", Result: strings.Clone(b)}
	for range 64 {
		tab.editResult(tab.Result + "x")
	}
	perfTab = tab
	runtime.GC()
	runtime.ReadMemStats(&after)
	undos := 0
	for tab.undoResult(false) {
		undos++
	}
	redos := 0
	for tab.undoResult(true) {
		redos++
	}
	if undos < 8 || undos != redos || tab.Result != b+strings.Repeat("x", 64) {
		t.Fatalf("history correctness undo=%d redo=%d", undos, redos)
	}
	t.Logf("retained heap delta=%d bytes, undo=%d redo=%d", int64(after.HeapAlloc)-int64(before.HeapAlloc), undos, redos)
	perfTab = nil
}

func BenchmarkPerfCaret(b *testing.B) {
	value := perfText(b, 512<<10)
	text := scene.NewText2D(value)
	text.Font = perfFont(b)
	count := utf8.RuneCountInString(value)
	if _, _, _, _, ok := scene.TextCaretRect2D(&text, value, count); !ok {
		b.Fatal("layout")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		scene.TextCaretRect2D(&text, value, count-1)
		scene.TextCaretRect2D(&text, value, count)
	}
}

// This measures workbench update and layout, excluding native presentation.
func BenchmarkPerfEditorFrame(b *testing.B) {
	app, session := newPencodeTestApp(b)
	model := session.active.model
	original := model.activeContent()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		value := original + "x"
		if model.activeContent() != original {
			value = original
		}
		model.updateActiveContent(value)
		model.setEditorValueAndCaret(model.fontResources, value, utf8.RuneCountInString(value))
		model.refreshRegion(model.fontResources, pencodeRefreshEditor)
		app.RequestRender()
		if _, err := app.Step(); err != nil {
			b.Fatal(err)
		}
	}
}

func perfFont(b testing.TB) *onixtext.Font {
	b.Helper()
	name := os.Getenv("ONIX_PERF_FONT")
	if name == "" {
		name = "../../../testdata/fonts/Noto_Sans_SC/static/NotoSansSC-Regular.ttf"
	}
	data, err := os.ReadFile(name)
	if err != nil {
		b.Fatal(err)
	}
	font, err := onixtext.LoadFont(data)
	if err != nil {
		b.Fatal(err)
	}
	return font
}
