package workbench

import (
	"math"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/onix-engine/onix/ecs"
	"github.com/onix-engine/onix/graphics"
	graphicssvg "github.com/onix-engine/onix/graphics/codec/svg"
	"github.com/onix-engine/onix/module"
	"github.com/onix-engine/onix/render"
	"github.com/onix-engine/onix/scene"
	"github.com/onix-engine/onix/text/piecetree"
	"github.com/pencode-labs/pencode/lsp"
)

type pencodeDiagnosticRange struct {
	message    string
	start, end int
	severity   int
}

type pencodeUnusedRange struct{ start, end int }

type pencodeDiagnosticPaint struct {
	waveTexture          *graphics.Texture
	file                 *openFile
	content              piecetree.Snapshot
	quads, labels, waves []ecs.Entity
	waveViews            []*graphics.Texture
	ranges               []pencodeDiagnosticRange
	unused               []pencodeUnusedRange
	unusedSpans          []scene.TextSpan2D
	underlines           []scene.TextRangeRect2D
	unusedKey            pencodeOverlayKey
	key                  pencodeDiagnosticPaintKey
	root                 ecs.Entity
	revision             uint64
}

type pencodeDiagnosticPaintKey struct {
	file                     *openFile
	layout                   pencodeGutterLayoutKey
	revision                 uint64
	stack                    scene.UIStackIndex
	colors                   [len(pencodeProblemSeverities)]graphics.ColorRGBA
	color                    graphics.ColorRGBA
	x, y, width, height      float32
	spacing, lineHeight, ink float32
}

func (paint *pencodeDiagnosticPaint) syncRanges(model *workspaceModel, file *openFile) {
	if paint.file != file || paint.content != file.snapshot() || paint.revision != model.bottom.diagnosticRevision {
		clear(paint.ranges)
		paint.ranges = paint.ranges[:0]
		paint.unused = paint.unused[:0]
		paint.unusedKey = pencodeOverlayKey{}
		filename := path.Clean(filepath.ToSlash(filepath.Join(model.projectPath, filepath.FromSlash(file.Path))))
		for key, diagnostics := range model.bottom.diagnostics {
			// Workspace diagnostics can arrive before the file has an LSP document.
			// Compare decoded paths so server URI escaping does not affect identity.
			diagnosticPath, err := key.uri.Path()
			if err != nil {
				continue
			}
			diagnosticPath = path.Clean(diagnosticPath)
			matches := diagnosticPath == filename
			if len(filename) > 2 && filename[1] == ':' {
				matches = strings.EqualFold(diagnosticPath, filename)
			}
			if !matches {
				continue
			}
			encoding := lsp.UTF16
			if key.session.client != nil {
				encoding = key.session.client.Encoding
			}
			for _, diagnostic := range diagnostics {
				severity := diagnostic.Severity
				if severity < 1 || severity >= len(pencodeProblemSeverities) {
					severity = 3
				}
				start, startErr := lsp.OffsetAt(file.snapshot(), diagnostic.Range.Start, encoding)
				end, endErr := lsp.OffsetAt(file.snapshot(), diagnostic.Range.End, encoding)
				if startErr != nil || endErr != nil || end < start {
					continue
				}
				if slices.Contains(diagnostic.Tags, lsp.DiagnosticTagUnnecessary) {
					if start < end {
						paint.unused = append(paint.unused, pencodeUnusedRange{start, end})
					}
					if severity == lsp.DiagnosticSeverityHint {
						continue
					}
				}
				message := strings.ReplaceAll(strings.ReplaceAll(diagnostic.Message, "\r", " "), "\n", " ")
				paint.ranges = append(paint.ranges, pencodeDiagnosticRange{start: start, end: end, message: message, severity: severity})
			}
		}
		slices.SortFunc(paint.ranges, func(a, b pencodeDiagnosticRange) int {
			// Paint the most severe underline last when diagnostic ranges overlap.
			if a.severity != b.severity {
				return b.severity - a.severity
			}
			if a.start != b.start {
				return a.start - b.start
			}
			if a.end != b.end {
				return a.end - b.end
			}
			return strings.Compare(a.message, b.message)
		})
		slices.SortFunc(paint.unused, func(a, b pencodeUnusedRange) int { return a.start - b.start })
		merged := paint.unused[:0]
		for _, span := range paint.unused {
			if len(merged) > 0 && span.start <= merged[len(merged)-1].end {
				merged[len(merged)-1].end = max(merged[len(merged)-1].end, span.end)
			} else {
				merged = append(merged, span)
			}
		}
		paint.unused = merged
		paint.file, paint.content, paint.revision = file, file.snapshot(), model.bottom.diagnosticRevision
	}
}

func (paint *pencodeDiagnosticPaint) fadeUnused(model *workspaceModel, foreground graphics.ColorRGBA, spans []scene.TextSpan2D, key pencodeOverlayKey) ([]scene.TextSpan2D, pencodeOverlayKey) {
	file := model.activeFile()
	if file == nil || key.source != file.snapshot() || key.theme == nil || key.theme.unusedOpacity == 1 {
		return spans, key
	}
	paint.syncRanges(model, file)
	if len(paint.unused) == 0 {
		return spans, key
	}
	key.diagnostics, key.unusedForeground = paint.revision, foreground
	if paint.unusedKey != key {
		paint.unusedSpans = fadeUnusedSpans(paint.unusedSpans[:0], spans, paint.unused, key.source.Len(), foreground, key.theme.unusedOpacity)
		paint.unusedKey = key
	}
	return paint.unusedSpans, key
}

// Sweep color spans and unused ranges together, using the editor foreground in gaps.
func fadeUnusedSpans(dst, base []scene.TextSpan2D, unused []pencodeUnusedRange, length int, foreground graphics.ColorRGBA, opacity float32) []scene.TextSpan2D {
	for start := 0; start < length && (len(base) > 0 || len(unused) > 0); {
		end, color := length, foreground
		styled := len(base) > 0 && base[0].Start <= start
		if styled {
			end, color = base[0].End, base[0].Color
		} else if len(base) > 0 {
			end = base[0].Start
		}
		faded := len(unused) > 0 && unused[0].start <= start
		if faded {
			end = min(end, unused[0].end)
			color.A *= opacity
		} else if len(unused) > 0 {
			end = min(end, unused[0].start)
		}
		if (styled || faded) && start < end {
			dst = appendPencodeSpan(dst, scene.TextSpan2D{Start: start, End: end, Color: color})
		}
		if styled && base[0].End == end {
			base = base[1:]
		}
		if faded && unused[0].end == end {
			unused = unused[1:]
		}
		start = end
	}
	return dst
}

func (paint *pencodeDiagnosticPaint) sync(world *module.Content, model *workspaceModel) {
	file := model.activeFile()
	text := world.Mut[scene.Text2D](model.editor.text)
	visible := model.screen == pencodeWorkspace && !model.settingsOpen && model.textEditorActive() && file != nil &&
		text != nil && text.Text() == file.snapshot() && model.workbenchTheme != nil
	if !visible {
		paint.hide(world)
		return
	}
	paint.syncRanges(model, file)
	if len(paint.ranges) == 0 {
		paint.hide(world)
		return
	}
	transform, _ := world.Get[scene.UITransform](model.editor.text)
	size, _ := world.Get[scene.Size2D](model.editor.control)
	stack, _ := world.Get[scene.UIStackIndex](model.editor.text)
	key := pencodeDiagnosticPaintKey{
		layout: pencodeGutterLayoutKey{value: text.Text(), font: text.Font, fontSize: text.FontSize, dpi: text.DPI, wrapWidth: text.WrapWidth, tabWidth: text.TabWidth, wrapMode: text.WrapMode},
		file:   file, revision: paint.revision, x: transform.Translation.X.Value, y: transform.Translation.Y.Value,
		width: size.Width, height: size.Height, spacing: text.LetterSpacing, lineHeight: text.LineHeight,
		ink: text.InkOffsetY, stack: stack, colors: model.workbenchTheme.diagnosticColors,
	}
	if paint.key == key {
		return
	}
	count := scene.TextLineCount2D(text)
	if count == 0 || text.LineHeight <= 0 {
		return // Retry once the editor's font and layout are available.
	}
	if paint.waveTexture == nil {
		data, err := world.ReadFile("assets/ui/squiggly.svg")
		if err != nil {
			return
		}
		paint.waveTexture, err = graphicssvg.DecodeAtScale(data, 4, graphics.TextureOptions{Usage: graphics.ResourceUsageDefault, WrapU: render.TextureAddressRepeat})
		if err != nil {
			return
		}
	}
	if !world.IsAlive(paint.root) {
		bundle := ecs.NewBundle(7)
		bundle.Put(scene.UITransformFromXY(0, 0))
		bundle.Put(scene.UIGlobalTransform{})
		bundle.Put(scene.Parent{Entity: model.editor.control})
		bundle.Put(scene.AnchorTopLeft)
		bundle.Put(scene.Visibility{})
		bundle.Put(scene.ClipRect2D{})
		bundle.Put(scene.Size2D{})
		paint.root = world.Spawn(bundle)
	}
	world.SetIfChanged(paint.root, scene.Parent{Entity: model.editor.control})
	world.SetIfChanged(paint.root, scene.Visibility{Visible: true})
	world.SetIfChanged(paint.root, scene.ClipRect2D{Width: size.Width, Height: size.Height})
	world.SetIfChanged(paint.root, size)
	quadCount, labelCount, waveCount := 0, 0, 0
	first := max(0, int(math.Floor(float64(-key.y/text.LineHeight))))
	for line := first; line < count; line++ {
		start, end, rect, _ := scene.TextLineRange2D(text, line)
		y := key.y + rect.Y
		if y >= size.Height {
			break
		}
		marked := -1
		for i, diagnostic := range paint.ranges {
			if diagnostic.start > end || diagnostic.end < start || diagnostic.end == start && diagnostic.start != start {
				continue
			}
			if diagnostic.start == end && start < end && end < text.Text().Len() && text.Text().ByteAt(end) != '\r' && text.Text().ByteAt(end) != '\n' {
				continue // A soft wrap boundary belongs to the following visual line.
			}
			if marked < 0 || diagnostic.severity < paint.ranges[marked].severity {
				marked = i
			}
			left, right := max(start, diagnostic.start), min(end, diagnostic.end)
			if left == right && right < end {
				_, length := text.Text().DecodeRune(right)
				right += length
			}
			paint.underlines = paint.underlines[:0]
			var underline scene.TextRangeRect2D
			if left < right {
				paint.underlines, underline, _ = scene.AppendTextUnderlineRects2D(paint.underlines, text, line, left, right, 1)
			} else {
				underline = scene.TextRangeRect2D{X: rect.X + rect.Width, Y: rect.Y + rect.Height - 3, Width: float32(text.FontSize) * .6}
			}
			x := key.x + underline.X
			waveLeft, waveRight := max(x, 0), min(x+underline.Width, size.Width)
			if waveRight > waveLeft {
				paint.wave(world, &waveCount, stack+1, waveLeft, y+rect.Height-3, waveRight-waveLeft, (waveLeft-x)/6, (waveRight-x)/6, key.colors[diagnostic.severity])
			}
		}
		if marked >= 0 {
			diagnostic := paint.ranges[marked]
			color := key.colors[diagnostic.severity]
			background := color
			background.A *= .10
			paint.quad(world, &quadCount, stack-1, 0, y, size.Width, rect.Height, 0, background)
			paint.label(world, &labelCount, stack+1, key.x+rect.X+rect.Width+float32(text.FontSize)*2, y, diagnostic.message, text, color)
		}
	}
	for _, entity := range paint.quads[quadCount:] {
		world.SetIfChanged(entity, scene.Visibility{})
	}
	for _, entity := range paint.labels[labelCount:] {
		world.SetIfChanged(entity, scene.Visibility{})
	}
	for _, entity := range paint.waves[waveCount:] {
		world.SetIfChanged(entity, scene.Visibility{})
	}
	paint.key = key
}

func (paint *pencodeDiagnosticPaint) hide(world *module.Content) {
	if paint.key.file == nil {
		return
	}
	world.SetIfChanged(paint.root, scene.Visibility{})
	for _, entity := range paint.quads {
		world.SetIfChanged(entity, scene.Visibility{})
	}
	for _, entity := range paint.labels {
		world.SetIfChanged(entity, scene.Visibility{})
	}
	for _, entity := range paint.waves {
		world.SetIfChanged(entity, scene.Visibility{})
	}
	paint.key = pencodeDiagnosticPaintKey{}
}

// Retain native entities so scrolling and repeated diagnostic updates reuse storage.
func pencodeDiagnosticEntity[T any](paint *pencodeDiagnosticPaint, world *module.Content, pool *[]ecs.Entity, used *int, stack scene.UIStackIndex, visual T) ecs.Entity {
	if *used == len(*pool) {
		bundle := ecs.NewBundle(8)
		bundle.Put(scene.UITransformFromXY(0, 0))
		bundle.Put(scene.UIGlobalTransform{})
		bundle.Put(scene.Parent{Entity: paint.root})
		bundle.Put(scene.AnchorTopLeft)
		bundle.Put(scene.Visibility{})
		bundle.Put(scene.Size2D{})
		bundle.Put(stack)
		bundle.Put(visual)
		*pool = append(*pool, world.Spawn(bundle))
	}
	entity := (*pool)[*used]
	*used++
	world.SetIfChanged(entity, stack)
	world.SetIfChanged(entity, scene.Visibility{Visible: true})
	return entity
}

func (paint *pencodeDiagnosticPaint) quad(world *module.Content, used *int, stack scene.UIStackIndex, x, y, width, height, angle float32, color graphics.ColorRGBA) {
	entity := pencodeDiagnosticEntity(paint, world, &paint.quads, used, stack, scene.NewSprite2D())
	transform := scene.UITransformFromXY(x, y)
	transform.Rotation = angle
	world.SetIfChanged(entity, transform)
	world.SetIfChanged(entity, scene.Size2D{Width: width, Height: height})
	sprite := scene.NewSprite2D()
	sprite.Width, sprite.Height, sprite.Shape = width, height, scene.RoundedRectShape2D(color, 0)
	world.SetIfChanged(entity, sprite)
}

func (paint *pencodeDiagnosticPaint) label(world *module.Content, used *int, stack scene.UIStackIndex, x, y float32, message string, source *scene.Text2D, color graphics.ColorRGBA) {
	entity := pencodeDiagnosticEntity(paint, world, &paint.labels, used, stack, scene.NewText2D(""))
	world.SetIfChanged(entity, scene.UITransformFromXY(x, y))
	text := world.Mut[scene.Text2D](entity)
	text.Value, text.Font, text.FontSize, text.DPI = message, source.Font, source.FontSize, source.DPI
	text.LineHeight, text.InkOffsetY, text.LetterSpacing = source.LineHeight, source.InkOffsetY, source.LetterSpacing
	text.WrapMode, text.Color = scene.TextWrapNone, color
}

func (paint *pencodeDiagnosticPaint) wave(world *module.Content, used *int, stack scene.UIStackIndex, x, y, width, u, maxU float32, color graphics.ColorRGBA) {
	index := *used
	entity := pencodeDiagnosticEntity(paint, world, &paint.waves, used, stack, scene.NewSprite2D())
	if index == len(paint.waveViews) {
		paint.waveViews = append(paint.waveViews, graphics.NewSubTexture(paint.waveTexture, 6, 3, u, 0, maxU, 1, 0))
	}
	view := paint.waveViews[index]
	view.U, view.MaxU = u, maxU
	world.SetIfChanged(entity, scene.UITransformFromXY(x, y))
	world.SetIfChanged(entity, scene.Size2D{Width: width, Height: 3})
	sprite := scene.NewSprite2D()
	sprite.Texture, sprite.Tint, sprite.Width, sprite.Height = view, color, width, 3
	// UVs can change while a clipped sprite keeps the same position and size.
	*world.Mut[scene.Sprite2D](entity) = sprite
}

func (paint *pencodeDiagnosticPaint) dispose(world *module.Content) {
	for _, entity := range paint.quads {
		world.Despawn(entity)
	}
	for _, entity := range paint.labels {
		world.Despawn(entity)
	}
	for _, entity := range paint.waves {
		world.Despawn(entity)
	}
	for _, view := range paint.waveViews {
		_ = view.Dispose()
	}
	if paint.waveTexture != nil {
		_ = paint.waveTexture.Dispose()
	}
	world.Despawn(paint.root)
	*paint = pencodeDiagnosticPaint{}
}
