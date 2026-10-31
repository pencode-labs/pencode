package workbench

import (
	"math"
	"strconv"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/graphics"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	"github.com/diiyw/onix/text"
	"github.com/diiyw/onix/text/piecetree"
	engineui "github.com/diiyw/onix/ui"
	"github.com/diiyw/onix/ui/html"
)

type pencodeEditor struct {
	control, text, gutter, numbers      ecs.Entity
	file                                *openFile
	needsFocus                          bool
	guide                               ecs.Entity
	firstLine, lastLine                 int
	numberBuffer                        []byte
	sourceLines                         []int
	compositionSource, compositionValue piecetree.Snapshot
	compositionText                     string
	compositionCaret                    int
	compositionRuneCaret                int
	compositionSpans                    []scene.TextSpan2D
	compositionPaint                    pencodeOverlayKey
	linkSpans                           []scene.TextSpan2D
	linkPaint                           pencodeOverlayKey
	brackets                            pencodeBracketIndex
	diagnostics                         pencodeDiagnosticPaint
	debugPaint                          pencodeDebugPaint
	findPaint                           pencodeFindPaint
	layoutKey                           pencodeGutterLayoutKey
	caretContent                        piecetree.Snapshot
	caretInfo                           string
	caretIndex, caretLine               int
}

type pencodeGutterLayoutKey struct {
	value               piecetree.Snapshot
	font                *text.Font
	fontSize, dpi       float64
	wrapWidth, tabWidth float32
	wrapMode            scene.TextWrapMode
}

type pencodeEditorView struct {
	caret, anchor    int
	scrollX, scrollY float32
}

// Snapshot after native input and layout, before a tab event can replace the
// shared control's value and clamp its selection to another document.
func (editor *pencodeEditor) saveView(world *module.Content, model *workspaceModel) {
	if editor.file == nil || editor.file != model.activeFile() || !model.textEditorActive() {
		return
	}
	field, ok := world.Get[engineui.TextField](editor.control)
	textarea, scrollOK := world.Get[engineui.Textarea](editor.control)
	if ok && scrollOK {
		editor.file.editorView = pencodeEditorView{field.Caret, field.Anchor, textarea.ScrollX, textarea.ScrollY}
		if model.findOpen && !model.findSelection && field.Focused && field.Caret != field.Anchor {
			model.captureFindScope(world)
		}
	}
}

const pencodeEditorWheelLines = 4

func syncPencodeEditor(context *module.ContentSystemContext) error {
	world := context.World
	var model *workspaceModel
	var surface ecs.Entity
	for _, access := range world.Query[html.Component[appProps, appState]]() {
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
		world.SetIfChanged(entity, scene.Visibility{Visible: visible})
	}
	if !visible {
		editor.file = nil
		editor.needsFocus = false
		world.SetIfChanged(editor.guide, scene.Visibility{})
		model.focusFindInput(world, surface)
		editor.syncCaretStatus(world, surface, model)
		return
	}
	if !world.IsAlive(editor.control) {
		editor.control, editor.text = 0, 0
		editor.control, _ = world.HTMLControl(surface, "editor")
		if editor.control != 0 {
			_ = world.Insert(editor.control, engineui.TextFieldKeyHandler{Handle: model.editorKeyHandled})
		}
	}
	if !world.IsAlive(editor.text) {
		editor.text, _ = world.FindPart[engineui.ValuePart, scene.Text2D](editor.control)
	}
	if model.workbenchTheme != nil {
		if world.Has[engineui.StyleOverride[engineui.TextFieldStyle]](editor.control) {
			world.SetIfChanged(editor.control, model.workbenchTheme.editorStyle)
		} else {
			_ = world.Insert(editor.control, model.workbenchTheme.editorStyle)
		}
	}
	font := model.codeFont()
	if binding := world.Mut[html.ControlTextFont](editor.control); binding != nil {
		binding.Font = font
	} else {
		_ = world.Insert(editor.control, html.ControlTextFont{Font: font})
	}
	valueText := world.Mut[scene.Text2D](editor.text)
	ok := valueText != nil
	if !ok {
		return
	}
	valueText.Index = file.snapshotBuffer()
	if font != nil {
		valueText.Font = font
	}
	if width, ok := world.ControlTextTabWidth(editor.control, valueText); ok {
		valueText.TabWidth = width
	}
	if textarea := world.Mut[engineui.Textarea](editor.control); textarea != nil {
		textarea.CaptureTab = true
		textarea.WheelStep = valueText.LineHeight * pencodeEditorWheelLines
		textarea.WrapTokens = true
		textarea.SelectTokens = true
		textarea.WrapWidthPercent = float32(model.settings.EditorWidth)
		if link, ok := world.Get[html.ControlText](editor.text); ok {
			valueText.WrapMode = scene.TextWrapTokens
			valueText.WrapWidth = link.Content.Width * textarea.WrapWidthPercent / 100
		}
	}
	guideColor := graphics.ColorRGBA{}
	if model.workbenchTheme != nil {
		guideColor = model.workbenchTheme.guide
	}
	editor.syncWidthGuide(world, model.settings.EditorWidth, guideColor)
	field, _ := world.Get[engineui.TextField](editor.control)
	if content := model.activeSnapshot(); field.Buffer != &file.buffer || content != field.Text() {
		model.bindEditorSnapshot(world, content)
	}
	model.focusFindInput(world, surface)
	if editor.file != file || editor.needsFocus {
		editor.needsFocus = false
		if !model.folderSearch.open && !model.quickOpen && !model.findOpen && !model.settingsOpen && model.closing.action == pencodeCloseNone {
			// A comparison tab can still hide this control until HTML reconciles.
			// Retry on the next frame once the file editor is interactive again.
			editor.needsFocus = !world.Focus(editor.control)
			if editor.needsFocus && model.app != nil {
				model.app.RequestRender()
			}
		}
		// Focus can move the caret to the end; restore the document after it.
		if field := world.Mut[engineui.TextField](editor.control); field != nil {
			length := file.snapshotBuffer().Runes()
			field.Caret, field.Anchor = min(file.editorView.caret, length), min(file.editorView.anchor, length)
			field.ScrollX = 0
			field.Composition = ""
			field.Selecting = false
		}
		if textarea := world.Mut[engineui.Textarea](editor.control); textarea != nil {
			textarea.RestoreScroll(file.editorView.scrollX, file.editorView.scrollY)
		}
		if link, ok := world.Get[html.ControlText](editor.text); ok {
			transform := world.Mut[scene.UITransform](editor.text)
			transform.Translation.X.Value, transform.Translation.Y.Value = link.Content.X-file.editorView.scrollX, link.Content.Y-file.editorView.scrollY
		}
		editor.file = file
	}
	if field := world.Mut[engineui.TextField](editor.control); field != nil {
		field.Buffer = file.snapshotBuffer()
		field.Source = file.snapshot()
		field.Value = ""
	}
	field, _ = world.Get[engineui.TextField](editor.control)
	editor.syncHighlight(valueText, field, model)
	editor.syncCaretStatus(world, surface, model)
}

func (editor *pencodeEditor) syncCaretStatus(world *module.Content, surface ecs.Entity, model *workspaceModel) {
	field, ok := world.Get[engineui.TextField](editor.control)
	visible := ok && model.screen == pencodeWorkspace && model.textEditorActive()
	if visible && editor.caretLine != 0 && editor.caretContent == field.Text() && editor.caretIndex == field.Caret {
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
	editor.caretContent, editor.caretIndex, editor.caretLine = piecetree.Snapshot{}, 0, 0
	if visible {
		document := model.activeFile().snapshotBuffer()
		line, column := document.Position(document.ByteOffset(field.Caret))
		line, column = line+1, column+1
		var buffer [64]byte
		value := append(buffer[:0], "Ln "...)
		value = strconv.AppendInt(value, int64(line), 10)
		value = append(value, ", Col "...)
		value = strconv.AppendInt(value, int64(column), 10)
		label = string(value)
		editor.caretContent, editor.caretIndex, editor.caretLine = field.Text(), field.Caret, line
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
	if model.highlightedContent == field.Text() {
		spans = model.languageHighlight(model.highlightScratch)
	}
	if field.Composition == "" {
		hover := &model.languageUI.hover
		if hover.link && hover.file == model.activeFile() && hover.content == field.Text() && hover.start >= 0 && hover.start < hover.end && hover.end <= field.Text().Len() && model.workbenchTheme != nil {
			link := [...]scene.TextSpan2D{{Start: hover.start, End: hover.end, Color: model.workbenchTheme.link}}
			key := pencodeOverlayPaintKey(model, field.Text(), spans, hover.start, hover.end)
			if key != editor.linkPaint {
				editor.linkSpans = overlayLanguageSpans(editor.linkSpans[:0], spans, link[:])
				editor.linkPaint = key
			}
			spans = editor.linkSpans
		}
	}
	spans = editor.brackets.highlight(field, model, spans)
	editor.syncPaint(valueText, field, spans, pencodeOverlayPaintKey(model, field.Text(), spans, editor.brackets.paint.start, editor.brackets.paint.end))
}

func (editor *pencodeEditor) syncPaint(valueText *scene.Text2D, field engineui.TextField, spans []scene.TextSpan2D, paint pencodeOverlayKey) {
	valueText.Value = ""
	source := field.Text()
	if field.Composition == "" {
		valueText.Source, valueText.Spans = source, spans
		editor.compositionSource, editor.compositionValue = piecetree.Snapshot{}, piecetree.Snapshot{}
		editor.compositionText = ""
		editor.compositionPaint = pencodeOverlayKey{}
		return
	}
	start, end := field.Caret, field.Anchor
	if start > end {
		start, end = end, start
	}
	prefix, oldEnd := source.ByteOffset(start), source.ByteOffset(end)
	changed := editor.compositionSource != source || editor.compositionText != field.Composition || editor.compositionCaret != prefix || editor.compositionRuneCaret != end
	if changed {
		editor.compositionValue = source.Replace(prefix, oldEnd, field.Composition)
		editor.compositionSource, editor.compositionText, editor.compositionCaret = source, field.Composition, prefix
		editor.compositionRuneCaret = end
	}
	valueText.Source = editor.compositionValue
	if changed || editor.compositionPaint != paint {
		editor.compositionSpans = rebasePencodeSpans(editor.compositionSpans[:0], spans, prefix, oldEnd, prefix+len(field.Composition))
		editor.compositionPaint = paint
	}
	valueText.Spans = editor.compositionSpans
}

func (editor *pencodeEditor) syncGutter(world *module.Content, surface ecs.Entity, model *workspaceModel) {
	visible := model.screen == pencodeWorkspace && model.textEditorActive()
	if !visible {
		return
	}
	valueText := world.Mut[scene.Text2D](editor.text)
	if valueText == nil {
		return
	}
	valueTransform, _ := world.Get[scene.UITransform](editor.text)
	if !world.IsAlive(editor.gutter) {
		bundle := ecs.NewBundle(6)
		bundle.Put(scene.UITransformFromXY(0, 0))
		bundle.Put(scene.UIGlobalTransform{})
		bundle.Put(scene.Parent{Entity: surface})
		bundle.Put(scene.ClipRect2D{})
		bundle.Put(scene.Visibility{})
		editor.gutter = world.Spawn(bundle)
		bundle = ecs.NewBundle(7)
		bundle.Put(scene.UITransformFromXY(14, 0))
		bundle.Put(scene.UIGlobalTransform{})
		bundle.Put(scene.Parent{Entity: editor.gutter})
		bundle.Put(scene.AnchorTopLeft)
		bundle.Put(scene.Visibility{Visible: true})
		bundle.Put(scene.NewText2D(""))
		stack, _ := world.Get[scene.UIStackIndex](editor.text)
		bundle.Put(stack)
		editor.numbers = world.Spawn(bundle)
	}
	stack, _ := world.Get[scene.UIStackIndex](editor.text)
	world.SetIfChanged(editor.numbers, stack)
	visibility := world.Mut[scene.Visibility](editor.gutter)
	visibility.Visible = visible
	gutterNode, ok := world.HTMLNode(surface, "editor-gutter")
	if !ok {
		return
	}
	bounds, ok := gutterNode.Bounds()
	if !ok {
		return
	}
	transform := world.Mut[scene.UITransform](editor.gutter)
	transform.Translation.X.Value, transform.Translation.Y.Value = float32(bounds.X), float32(bounds.Y)
	clip := world.Mut[scene.ClipRect2D](editor.gutter)
	clip.Width, clip.Height = float32(bounds.Width), float32(bounds.Height)
	numbers := world.Mut[scene.Text2D](editor.numbers)
	numbers.Font, numbers.FontSize, numbers.DPI = valueText.Font, valueText.FontSize, valueText.DPI
	numbers.LineHeight = valueText.LineHeight
	// Share the font's centered line metrics with code and diagnostic labels.
	numbers.InkOffsetY = valueText.InkOffsetY
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
	key := pencodeGutterLayoutKey{valueText.Text(), valueText.Font, valueText.FontSize, valueText.DPI, valueText.WrapWidth, valueText.TabWidth, valueText.WrapMode}
	first = min(first, count)
	last := min(count, max(first, int(math.Ceil(float64(bounds.Height-originY)/float64(lineHeight)))))
	if key != editor.layoutKey || first != editor.firstLine || last != editor.lastLine {
		editor.sourceLines = editor.sourceLines[:0]
		document := model.activeFile().snapshotBuffer()
		for index := first; index < last; index++ {
			start, _, _, _ := scene.TextLineRange2D(valueText, index)
			number := 0
			if index == 0 || start > 0 && valueText.Text().ByteAt(start-1) == '\n' {
				line := 0
				if document.MatchesSnapshot(valueText.Text()) {
					line, _ = document.Position(start)
				} else {
					line, _ = valueText.Text().Position(start)
				}
				number = line + 1
			}
			editor.sourceLines = append(editor.sourceLines, number)
		}
		editor.layoutKey = key
	}

	buffer := editor.numberBuffer[:0]
	for line := first; line < last; line++ {
		if line > first {
			buffer = append(buffer, '\n')
		}
		if number := editor.sourceLines[line-first]; number != 0 {
			buffer = strconv.AppendInt(buffer, int64(number), 10)
		}
	}
	editor.numberBuffer = buffer
	if string(buffer) != numbers.Value {
		numbers.Value = string(buffer)
	}
	editor.firstLine, editor.lastLine = first, last
	numberTransform := world.Mut[scene.UITransform](editor.numbers)
	numberTransform.Translation.Y.Value = originY + float32(first)*lineHeight
}

func (editor *pencodeEditor) syncWidthGuide(world *module.Content, widthPercent int, color graphics.ColorRGBA) {
	link, ok := world.Get[html.ControlText](editor.text)
	if !ok {
		return
	}
	size, _ := world.Get[scene.Size2D](editor.control)
	stack, _ := world.Get[scene.UIStackIndex](editor.control)
	width := link.Content.Width * float32(widthPercent) / 100
	visibility := scene.Visibility{Visible: width > 0 && width < link.Content.Width}
	guideSize := scene.Size2D{Width: 1, Height: size.Height}
	if !world.IsAlive(editor.guide) {
		bundle := ecs.NewBundle(8)
		transform := scene.UITransformFromXY(link.Content.X+width, 0)
		bundle.Put(transform)
		bundle.Put(scene.UIGlobalTransform{})
		bundle.Put(scene.Parent{Entity: editor.control})
		bundle.Put(scene.AnchorTopLeft)
		bundle.Put(visibility)
		bundle.Put(guideSize)
		sprite := scene.NewSprite2D()
		sprite.Shape = scene.RoundedRectShape2D(color, 0)
		sprite.Width, sprite.Height = 1, size.Height
		bundle.Put(sprite)
		bundle.Put(stack)
		editor.guide = world.Spawn(bundle)
		return
	}
	world.SetIfChanged(editor.guide, stack)
	world.SetIfChanged(editor.guide, scene.Parent{Entity: editor.control})
	world.SetIfChanged(editor.guide, visibility)
	world.SetIfChanged(editor.guide, guideSize)
	transform := world.Mut[scene.UITransform](editor.guide)
	transform.Translation.X.Value = link.Content.X + width
	sprite := world.Mut[scene.Sprite2D](editor.guide)
	sprite.Shape.Fill = color
	sprite.Width, sprite.Height = 1, size.Height
}

func (editor *pencodeEditor) dispose(world *module.Content) {
	editor.debugPaint.dispose(world)
	editor.diagnostics.dispose(world)
	editor.findPaint.dispose(world)
	world.Despawn(editor.numbers)
	world.Despawn(editor.gutter)
	world.Despawn(editor.guide)
	*editor = pencodeEditor{}
}
