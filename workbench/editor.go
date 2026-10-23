package workbench

import (
	"math"
	"strconv"
	"unicode/utf8"

	"github.com/diiyw/onix/module"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/graphics"
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/scene"
	"github.com/diiyw/onix/text"
	engineui "github.com/diiyw/onix/ui"
	"github.com/diiyw/onix/ui/html"
)

type pencodeEditor struct {
	control, text, gutter, numbers                       ecs.Entity
	file                                                 *openFile
	guide                                                ecs.Entity
	firstLine, lastLine                                  int
	numberBuffer                                         []byte
	sourceLines                                          []int
	compositionSource, compositionText, compositionValue string
	compositionCaret                                     int
	compositionRuneCaret                                 int
	compositionSpans                                     []scene.TextSpan2D
	compositionPaint                                     pencodeOverlayKey
	linkSpans                                            []scene.TextSpan2D
	linkPaint                                            pencodeOverlayKey
	brackets                                             pencodeBracketIndex
	layoutKey                                            pencodeGutterLayoutKey
	caretContent                                         string
	caretInfo                                            string
	caretIndex, caretLine                                int
}

type pencodeGutterLayoutKey struct {
	value               string
	font                *text.Font
	fontSize, dpi       float64
	wrapWidth, tabWidth float32
	wrapMode            scene.TextWrapMode
}

const (
	pencodeCodeInkOffsetEm   = -.11
	pencodeNumberInkOffsetEm = -.065
	pencodeEditorWheelLines  = 4
)

func syncPencodeEditor(context *module.ContentSystemContext) error {
	world := context.World
	var model *workspaceModel
	var surface ecs.Entity
	for _, access := range module.ContentQuery[html.Component[appProps, appState]](world) {
		component := access.Ref()

		model, surface = component.Props.Model, component.Page
		break
	}
	if model != nil {
		model.editor.sync(world, surface, model)
	}
	return nil
}

func (editor *pencodeEditor) sync(world *module.Content, surface ecs.Entity, model *workspaceModel) {
	editor.syncText(world, surface, model)
	editor.syncGutter(world, surface, model)
}

// Bind the code font before HTML and native layout measure the editor text.
func (editor *pencodeEditor) syncText(world *module.Content, surface ecs.Entity, model *workspaceModel) {
	model.focusCloseDialog(world, surface)
	file := model.activeFile()
	visible := model.screen == pencodeWorkspace && file != nil && model.textEditorActive()
	for _, entity := range [...]ecs.Entity{editor.gutter, editor.numbers} {
		module.ContentSetIfChanged(world, entity, scene.Visibility{Visible: visible})
	}
	if !visible {
		if model.sourceState().visible() {
			editor.file = nil
		}
		module.ContentSetIfChanged(world, editor.guide, scene.Visibility{})
		model.focusFindInput(world, surface)
		editor.syncCaretStatus(world, surface, model)
		return
	}
	if !world.IsAlive(editor.control) {
		editor.control, editor.text = 0, 0
		editor.control, _ = world.HTMLControl(surface, "editor")
		if editor.control != 0 {
			_ = module.ContentInsert(world, editor.control, engineui.TextFieldKeyHandler{Handle: func(*input.State) bool { return model.languageUI.keysHandled }})
		}
	}
	if !world.IsAlive(editor.text) {
		editor.text, _ = module.ContentFindPart[engineui.ValuePart, scene.Text2D](world, editor.control)
	}
	if model.workbenchTheme != nil {
		if module.ContentHas[engineui.StyleOverride[engineui.TextFieldStyle]](world, editor.control) {
			module.ContentSetIfChanged(world, editor.control, model.workbenchTheme.editorStyle)
		} else {
			_ = module.ContentInsert(world, editor.control, model.workbenchTheme.editorStyle)
		}
	}
	font := model.codeFont()
	if binding := module.ContentMut[html.ControlTextFont](world, editor.control); binding != nil {
		binding.Font = font
	} else {
		_ = module.ContentInsert(world, editor.control, html.ControlTextFont{Font: font})
	}
	valueText := module.ContentMut[scene.Text2D](world, editor.text)
	ok := valueText != nil
	if !ok {
		return
	}
	if font != nil {
		valueText.Font = font
	}
	if width, ok := world.ControlTextTabWidth(editor.control, valueText); ok {
		valueText.TabWidth = width
	}
	if textarea := module.ContentMut[engineui.Textarea](world, editor.control); textarea != nil {
		textarea.CaptureTab = true
		textarea.WheelStep = valueText.LineHeight * pencodeEditorWheelLines
		textarea.WrapTokens = true
		textarea.SelectTokens = true
		textarea.WrapWidthPercent = float32(model.settings.EditorWidth)
		if link, ok := module.ContentGet[html.ControlText](world, editor.text); ok {
			valueText.WrapMode = scene.TextWrapTokens
			valueText.WrapWidth = link.Content.Width * textarea.WrapWidthPercent / 100
		}
	}
	guideColor := graphics.ColorRGBA{}
	if model.workbenchTheme != nil {
		guideColor = model.workbenchTheme.guide
	}
	editor.syncWidthGuide(world, model.settings.EditorWidth, guideColor)
	valueText.InkOffsetY = float32(valueText.FontSize) * pencodeCodeInkOffsetEm
	field, _ := module.ContentGet[engineui.TextField](world, editor.control)
	if content := model.activeContent(); content != field.Value {
		world.SetControlValue(editor.control, content)
	}
	model.focusFindInput(world, surface)
	if editor.file != file {
		if !model.quickOpen && !model.findOpen && !model.settingsOpen && model.closing.action == pencodeCloseNone {
			world.Focus(editor.control)
		}
		// Focus can move the caret to the end; initialize the document after it.
		if field := module.ContentMut[engineui.TextField](world, editor.control); field != nil {
			field.Caret, field.Anchor = 0, 0
			field.ScrollX = 0
			field.Composition = ""
			field.Selecting = false
		}
		if textarea := module.ContentMut[engineui.Textarea](world, editor.control); textarea != nil {
			textarea.ResetScroll()
		}
		if link, ok := module.ContentGet[html.ControlText](world, editor.text); ok {
			transform := module.ContentMut[scene.UITransform](world, editor.text)
			transform.Translation.X.Value, transform.Translation.Y.Value = link.Content.X, link.Content.Y
		}
		editor.file = file
	}
	if field := module.ContentMut[engineui.TextField](world, editor.control); field != nil {
		field.Buffer = file.textBuffer(file.Content)
	}
	field, _ = module.ContentGet[engineui.TextField](world, editor.control)
	editor.syncHighlight(valueText, field, model)
	editor.syncCaretStatus(world, surface, model)
}

func (editor *pencodeEditor) syncCaretStatus(world *module.Content, surface ecs.Entity, model *workspaceModel) {
	field, ok := module.ContentGet[engineui.TextField](world, editor.control)
	visible := ok && model.screen == pencodeWorkspace && model.textEditorActive()
	if visible && editor.caretLine != 0 && editor.caretContent == field.Value && editor.caretIndex == field.Caret {
		return
	}
	if !visible && editor.caretLine == 0 {
		return
	}
	node, ok := world.HTMLNode(surface, "caret-info")
	if !ok {
		return
	}
	label := ""
	editor.caretContent, editor.caretIndex, editor.caretLine = "", 0, 0
	if visible {
		document := model.activeFile().textBuffer(field.Value)
		line, column := document.Position(document.ByteOffset(field.Caret))
		line, column = line+1, column+1
		var buffer [64]byte
		value := append(buffer[:0], "Ln "...)
		value = strconv.AppendInt(value, int64(line), 10)
		value = append(value, ", Col "...)
		value = strconv.AppendInt(value, int64(column), 10)
		label = string(value)
		editor.caretContent, editor.caretIndex, editor.caretLine = field.Value, field.Caret, line
	}
	// Update only the status text; caret movement must not rebuild the file tree.
	editor.caretInfo = label
	node.SetText(label)
	if model.app != nil {
		model.app.RequestRender()
	}
}

func (editor *pencodeEditor) syncHighlight(valueText *scene.Text2D, field engineui.TextField, model *workspaceModel) {
	var spans []scene.TextSpan2D
	if model.highlightedContent == field.Value {
		spans = model.languageHighlight(model.highlightScratch)
	}
	if field.Composition == "" {
		hover := &model.languageUI.hover
		if hover.link && hover.file == model.activeFile() && hover.content == field.Value && hover.start >= 0 && hover.start < hover.end && hover.end <= len(field.Value) && model.workbenchTheme != nil {
			link := [...]scene.TextSpan2D{{Start: hover.start, End: hover.end, Color: model.workbenchTheme.link}}
			key := pencodeOverlayPaintKey(model, field.Value, spans, hover.start, hover.end)
			if key != editor.linkPaint {
				editor.linkSpans = overlayLanguageSpans(editor.linkSpans[:0], spans, link[:])
				editor.linkPaint = key
			}
			spans = editor.linkSpans
		}
	}
	spans = editor.brackets.highlight(field, model, spans)
	editor.syncPaint(valueText, field, spans, pencodeOverlayPaintKey(model, field.Value, spans, editor.brackets.paint.start, editor.brackets.paint.end))
}

func (editor *pencodeEditor) syncPaint(valueText *scene.Text2D, field engineui.TextField, spans []scene.TextSpan2D, paint pencodeOverlayKey) {
	if field.Composition == "" {
		valueText.Value, valueText.Spans = field.Value, spans
		editor.compositionSource, editor.compositionText, editor.compositionValue = "", "", ""
		editor.compositionPaint = pencodeOverlayKey{}
		return
	}
	// Native UI inserts preedit at a rune caret; colour ranges use byte offsets.
	prefix := editor.compositionCaret
	if editor.compositionSource != field.Value || editor.compositionRuneCaret != field.Caret {
		if field.Buffer != nil && field.Buffer.Matches(field.Value) {
			prefix = field.Buffer.ByteOffset(field.Caret)
		} else {
			prefix = 0
			for caret := max(0, field.Caret); caret > 0 && prefix < len(field.Value); caret-- {
				_, size := utf8.DecodeRuneInString(field.Value[prefix:])
				prefix += size
			}
		}
	}
	changed := editor.compositionSource != field.Value || editor.compositionText != field.Composition || editor.compositionCaret != prefix
	if changed {
		editor.compositionValue = field.Value[:prefix] + field.Composition + field.Value[prefix:]
		editor.compositionSource, editor.compositionText, editor.compositionCaret = field.Value, field.Composition, prefix
	}
	editor.compositionRuneCaret = field.Caret
	valueText.Value = editor.compositionValue
	if changed || editor.compositionPaint != paint {
		editor.compositionSpans = rebasePencodeSpans(editor.compositionSpans[:0], spans, prefix, prefix, prefix+len(field.Composition))
		editor.compositionPaint = paint
	}
	valueText.Spans = editor.compositionSpans
}

func (editor *pencodeEditor) syncGutter(world *module.Content, surface ecs.Entity, model *workspaceModel) {
	visible := model.screen == pencodeWorkspace && model.textEditorActive()
	if !visible {
		return
	}
	valueText := module.ContentMut[scene.Text2D](world, editor.text)
	if valueText == nil {
		return
	}
	valueTransform, _ := module.ContentGet[scene.UITransform](world, editor.text)
	if !world.IsAlive(editor.gutter) {
		bundle := ecs.NewBundle(6)
		ecs.Put(&bundle, scene.UITransformFromXY(0, 0))
		ecs.Put(&bundle, scene.UIGlobalTransform{})
		ecs.Put(&bundle, scene.Parent{Entity: surface})
		ecs.Put(&bundle, scene.ClipRect2D{})
		ecs.Put(&bundle, scene.Visibility{})
		editor.gutter = world.Spawn(bundle)
		bundle = ecs.NewBundle(7)
		ecs.Put(&bundle, scene.UITransformFromXY(14, 0))
		ecs.Put(&bundle, scene.UIGlobalTransform{})
		ecs.Put(&bundle, scene.Parent{Entity: editor.gutter})
		ecs.Put(&bundle, scene.AnchorTopLeft)
		ecs.Put(&bundle, scene.Visibility{Visible: true})
		ecs.Put(&bundle, scene.NewText2D(""))
		stack, _ := module.ContentGet[scene.UIStackIndex](world, editor.text)
		ecs.Put(&bundle, stack)
		editor.numbers = world.Spawn(bundle)
	}
	stack, _ := module.ContentGet[scene.UIStackIndex](world, editor.text)
	module.ContentSetIfChanged(world, editor.numbers, stack)
	visibility := module.ContentMut[scene.Visibility](world, editor.gutter)
	visibility.Visible = visible
	gutterNode, ok := world.HTMLNode(surface, "editor-gutter")
	if !ok {
		return
	}
	bounds, ok := gutterNode.Bounds()
	if !ok {
		return
	}
	transform := module.ContentMut[scene.UITransform](world, editor.gutter)
	transform.Translation.X.Value, transform.Translation.Y.Value = float32(bounds.X), float32(bounds.Y)
	clip := module.ContentMut[scene.ClipRect2D](world, editor.gutter)
	clip.Width, clip.Height = float32(bounds.Width), float32(bounds.Height)
	numbers := module.ContentMut[scene.Text2D](world, editor.numbers)
	numbers.Font, numbers.FontSize, numbers.DPI = valueText.Font, valueText.FontSize, valueText.DPI
	numbers.LineHeight = valueText.LineHeight
	numbers.InkOffsetY = float32(numbers.FontSize) * pencodeNumberInkOffsetEm
	model.ensureSyntaxTheme()
	numbers.Color = pencodeSyntaxColor(model.syntaxTheme.Color("editorLineNumber.foreground"))
	numbers.TextAlignment, numbers.WrapWidth = scene.TextAlignRight, float32(bounds.Width)-23
	lineHeight := numbers.LineHeight
	if lineHeight <= 0 {
		return
	}
	// Use the value visual's origin, including the textarea's native scroll.
	originY := valueTransform.Translation.Y.Value
	first := max(0, int(math.Floor(float64(-originY/lineHeight))))
	count := scene.TextLineCount2D(valueText)
	key := pencodeGutterLayoutKey{valueText.Value, valueText.Font, valueText.FontSize, valueText.DPI, valueText.WrapWidth, valueText.TabWidth, valueText.WrapMode}
	if key != editor.layoutKey || len(editor.sourceLines) != count {
		editor.sourceLines = editor.sourceLines[:0]
		sourceLine := 0
		for index := range count {
			start, _, _, _ := scene.TextLineRange2D(valueText, index)
			number := 0
			if index == 0 || start > 0 && valueText.Value[start-1] == '\n' {
				sourceLine++
				number = sourceLine
			}
			editor.sourceLines = append(editor.sourceLines, number)
		}
		editor.layoutKey = key
	}
	first = min(first, count)
	last := min(count, max(first, int(math.Ceil(float64(bounds.Height-originY)/float64(lineHeight)))))
	buffer := editor.numberBuffer[:0]
	for line := first; line < last; line++ {
		if line > first {
			buffer = append(buffer, '\n')
		}
		if number := editor.sourceLines[line]; number != 0 {
			buffer = strconv.AppendInt(buffer, int64(number), 10)
		}
	}
	editor.numberBuffer = buffer
	if string(buffer) != numbers.Value {
		numbers.Value = string(buffer)
	}
	editor.firstLine, editor.lastLine = first, last
	numberTransform := module.ContentMut[scene.UITransform](world, editor.numbers)
	numberTransform.Translation.Y.Value = originY + float32(first)*lineHeight
}

func (editor *pencodeEditor) syncWidthGuide(world *module.Content, widthPercent int, color graphics.ColorRGBA) {
	link, ok := module.ContentGet[html.ControlText](world, editor.text)
	if !ok {
		return
	}
	size, _ := module.ContentGet[scene.Size2D](world, editor.control)
	stack, _ := module.ContentGet[scene.UIStackIndex](world, editor.control)
	width := link.Content.Width * float32(widthPercent) / 100
	visibility := scene.Visibility{Visible: width > 0 && width < link.Content.Width}
	guideSize := scene.Size2D{Width: 1, Height: size.Height}
	if !world.IsAlive(editor.guide) {
		bundle := ecs.NewBundle(8)
		transform := scene.UITransformFromXY(link.Content.X+width, 0)
		ecs.Put(&bundle, transform)
		ecs.Put(&bundle, scene.UIGlobalTransform{})
		ecs.Put(&bundle, scene.Parent{Entity: editor.control})
		ecs.Put(&bundle, scene.AnchorTopLeft)
		ecs.Put(&bundle, visibility)
		ecs.Put(&bundle, guideSize)
		sprite := scene.NewSprite2D()
		sprite.Shape = scene.RoundedRectShape2D(color, 0)
		sprite.Width, sprite.Height = 1, size.Height
		ecs.Put(&bundle, sprite)
		ecs.Put(&bundle, stack)
		editor.guide = world.Spawn(bundle)
		return
	}
	module.ContentSetIfChanged(world, editor.guide, stack)
	module.ContentSetIfChanged(world, editor.guide, scene.Parent{Entity: editor.control})
	module.ContentSetIfChanged(world, editor.guide, visibility)
	module.ContentSetIfChanged(world, editor.guide, guideSize)
	transform := module.ContentMut[scene.UITransform](world, editor.guide)
	transform.Translation.X.Value = link.Content.X + width
	sprite := module.ContentMut[scene.Sprite2D](world, editor.guide)
	sprite.Shape.Fill = color
	sprite.Width, sprite.Height = 1, size.Height
}

func (editor *pencodeEditor) dispose(world *module.Content) {
	world.Despawn(editor.numbers)
	world.Despawn(editor.gutter)
	world.Despawn(editor.guide)
	*editor = pencodeEditor{}
}
