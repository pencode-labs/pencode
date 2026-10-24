package workbench

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/diiyw/onix/module"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/lsp"
	"github.com/pencode-labs/pencode/output"
)

type pencodePanel uint8

const (
	pencodePanelClosed pencodePanel = iota
	pencodePanelProblems
	pencodePanelOutput
	pencodePanelSourceControl
	pencodePanelTerminal
	pencodePanelExtension
	pencodePanelPorts
)

type pencodeDiagnosticKey struct {
	session *pencodeLanguageSession
	uri     lsp.URI
}

type pencodeProblem struct {
	session  *pencodeLanguageSession
	location lsp.Location
	path     string
	label    string
}

type pencodeProblemProps struct {
	Model   *workspaceModel
	Problem pencodeProblem
}

type pencodeProblemGroup struct {
	uri         lsp.URI
	path, count string
	start, end  int
	expanded    bool
	revision    uint64
}

type pencodeProblemGroupProps struct {
	Model *workspaceModel
	Theme *pencodeTheme
	Icons *pencodeFileIcons
	Group pencodeProblemGroup
}

type pencodeBottomPanel struct {
	active                           pencodePanel
	resizing                         bool
	resizeY, resizeHeight            float32
	heightStyle                      string
	diagnostics                      map[pencodeDiagnosticKey][]lsp.Diagnostic
	problemsDirty                    bool
	problems                         []pencodeProblem
	problemGroups                    []*pencodeProblemGroup
	problemGroupByURI                map[lsp.URI]*pencodeProblemGroup
	problemRevision                  uint64
	problemGroupsInitialized         bool
	count                            string
	channels                         []*output.Channel
	options                          []htmlui.VNode
	source                           *output.Channel
	filter, cachedFilter, outputText string
	cachedSource                     *output.Channel
	outputRevision, storeRevision    uint64
	lastStatus                       string
}

func (model *workspaceModel) outputStore() *output.Store {
	if model.output == nil {
		model.output = &output.Store{}
	}
	return model.output
}

func (model *workspaceModel) logOutput(source, message string) {
	if message == "" {
		return
	}
	c := model.outputStore().Channel(source)
	_, _ = c.Write([]byte(message + "\n"))
}

func (model *workspaceModel) captureOutputStatus() {
	if model.bottom.lastStatus == model.status {
		return
	}
	model.bottom.lastStatus = model.status
	model.logOutput("pencode", model.status)
}

func (model *workspaceModel) pollOutput() bool {
	model.captureOutputStatus()
	if model.output == nil {
		return false
	}
	revision := model.output.Revision()
	if model.bottom.storeRevision == revision {
		return false
	}
	model.bottom.storeRevision = revision
	return model.bottom.active == pencodePanelOutput
}

func languageMessageLevel(level int) string {
	switch level {
	case 1:
		return "Error"
	case 2:
		return "Warning"
	case 3:
		return "Info"
	case 5:
		return "Debug"
	default:
		return "Log"
	}
}

func (model *workspaceModel) setProblems(session *pencodeLanguageSession, uri lsp.URI, items []lsp.Diagnostic) {
	p := &model.bottom
	key := pencodeDiagnosticKey{session, uri}
	if len(items) == 0 {
		if _, ok := p.diagnostics[key]; !ok {
			return
		}
		delete(p.diagnostics, key)
	} else {
		if p.diagnostics == nil {
			p.diagnostics = make(map[pencodeDiagnosticKey][]lsp.Diagnostic)
		}
		p.diagnostics[key] = items
	}
	p.problemsDirty = true
}

func (model *workspaceModel) clearSessionProblems(session *pencodeLanguageSession) {
	for key := range model.bottom.diagnostics {
		if key.session == session {
			delete(model.bottom.diagnostics, key)
			model.bottom.problemsDirty = true
		}
	}
}

// Rebuild only on a diagnostic change; counting and row preparation share a pass.
func (model *workspaceModel) syncProblems() {
	p := &model.bottom
	if !p.problemsDirty {
		if p.count == "" {
			p.count = "0"
		}
		return
	}
	clear(p.problems)
	p.problems = p.problems[:0]
	for key, items := range p.diagnostics {
		path := string(key.uri)
		if absolute, err := key.uri.Path(); err == nil {
			path = absolute
			if relative, err := filepath.Rel(key.session.key.root, absolute); err == nil {
				path = filepath.ToSlash(relative)
			}
		}
		for _, item := range items {
			severity := "信息"
			switch item.Severity {
			case 1:
				severity = "错误"
			case 2:
				severity = "警告"
			case 4:
				severity = "提示"
			}
			source := item.Source
			if source == "" && key.session.server != nil {
				source = key.session.server.Label
			}
			label := fmt.Sprintf("%s · %d:%d · %s · %s", severity, item.Range.Start.Line+1, item.Range.Start.Character+1, item.Message, source)
			p.problems = append(p.problems, pencodeProblem{session: key.session, location: lsp.Location{URI: key.uri, Range: item.Range}, path: path, label: label})
		}
	}
	slices.SortFunc(p.problems, func(a, b pencodeProblem) int {
		if c := strings.Compare(string(a.location.URI), string(b.location.URI)); c != 0 {
			return c
		}
		if c := a.location.Range.Start.Line - b.location.Range.Start.Line; c != 0 {
			return c
		}
		if c := a.location.Range.Start.Character - b.location.Range.Start.Character; c != 0 {
			return c
		}
		return strings.Compare(a.label, b.label)
	})
	p.problemRevision++
	clear(p.problemGroups)
	p.problemGroups = p.problemGroups[:0]
	for start := 0; start < len(p.problems); {
		problem := p.problems[start]
		end := start + 1
		for end < len(p.problems) && p.problems[end].location.URI == problem.location.URI {
			end++
		}
		group := p.problemGroupByURI[problem.location.URI]
		if group == nil {
			if p.problemGroupByURI == nil {
				p.problemGroupByURI = make(map[lsp.URI]*pencodeProblemGroup)
			}
			group = &pencodeProblemGroup{uri: problem.location.URI}
			p.problemGroupByURI[group.uri] = group
		}
		group.path, group.count = problem.path, strconv.Itoa(end-start)
		group.start, group.end, group.revision = start, end, p.problemRevision
		p.problemGroups = append(p.problemGroups, group)
		start = end
	}
	for uri, group := range p.problemGroupByURI {
		if group.revision != p.problemRevision {
			delete(p.problemGroupByURI, uri)
		}
	}
	p.count = strconv.Itoa(len(p.problems))
	if len(p.problemGroups) == 0 {
		p.problemGroupsInitialized = false
	}
	p.problemsDirty = false
}

func newProblemGroupDefinition(content *module.Content) (*htmlui.Definition[pencodeProblemGroupProps, struct{}], error) {
	problemDefinition, err := newProblemDefinition(content)
	if err != nil {
		return nil, err
	}
	return module.ContentDefine[pencodeProblemGroupProps, struct{}](content,
		htmlui.NewTemplate(`<div class="pencode-problem-group" role="listitem"><button data-ref="toggle" type="button" class="pencode-problem-row pencode-problem-group-toggle"><img data-ref="arrow" class="pencode-problem-group-arrow" alt=""><img data-ref="icon" class="pencode-problem-file-icon" alt=""><span data-ref="path"></span><span data-ref="count" class="pencode-problem-count"></span></button><div data-ref="items" class="pencode-problem-group-items" role="list"></div></div>`), "",
		module.ContentBehavior[pencodeProblemGroupProps, struct{}]{
			Render: func(c module.ContentRenderContext[pencodeProblemGroupProps, struct{}], view *htmlui.View) {
				model, group := c.Props.Model, c.Props.Group
				view.Text("path", group.path)
				view.Text("count", group.count)
				source := model.fileIcons.source(filepath.Base(group.path), false)
				if source == "" {
					source = "icons/file-text.svg"
				}
				view.Attr("icon", "src", model.themedIcon(source, "icon.foreground"))
				arrow := "icons/chevron-right.svg"
				if group.expanded {
					arrow = "icons/chevron-down.svg"
				}
				view.Attr("arrow", "src", model.themedIcon(arrow, "icon.foreground"))
				view.Attr("toggle", "aria-expanded", boolAttribute(group.expanded))
				view.Attr("items", "aria-label", group.path)
				view.Event("toggle", htmlui.EventClick)
				setPanelVisible(view, "items", group.expanded)
				if group.expanded {
					for i := group.start; i < group.end; i++ {
						htmlui.AppendComponentChild(view, "items", uint64(i-group.start+1), problemDefinition, pencodeProblemProps{Model: model, Problem: model.bottom.problems[i]}, struct{}{})
					}
				}
			},
			HandleEvent: handleProblemGroupEvent,
		})
}

func handleProblemGroupEvent(c *module.ContentEventContext[pencodeProblemGroupProps, struct{}]) {
	if c.Ref != "toggle" {
		return
	}
	model := c.Props.Model
	if group := model.bottom.problemGroupByURI[c.Props.Group.uri]; group != nil {
		group.expanded = !group.expanded
		markPencodeRootDirtyByModel(c.World, model)
	}
}

func newProblemDefinition(content *module.Content) (*htmlui.Definition[pencodeProblemProps, struct{}], error) {
	return module.ContentDefine[pencodeProblemProps, struct{}](content,
		htmlui.NewTemplate(`<div class="pencode-problem-item" role="listitem"><button data-ref="problem" type="button" class="pencode-problem-row"></button></div>`), "",
		module.ContentBehavior[pencodeProblemProps, struct{}]{
			Render: func(c module.ContentRenderContext[pencodeProblemProps, struct{}], view *htmlui.View) {
				view.Text("problem", c.Props.Problem.label)
				view.Event("problem", htmlui.EventClick)
			},
			HandleEvent: handleProblemEvent,
		})
}

func handleProblemEvent(c *module.ContentEventContext[pencodeProblemProps, struct{}]) {
	if c.Ref != "problem" {
		return
	}
	model, problem := c.Props.Model, c.Props.Problem
	if model.languages == nil {
		return
	}
	model.languages.panel.session = problem.session
	model.navigateLanguageLocation(c.World, problem.location, nil)
	markPencodeRootDirtyByModel(c.World, model)
}

func (model *workspaceModel) renderBottomPanel(view *htmlui.View, definition *htmlui.Definition[pencodeProblemGroupProps, struct{}]) {
	model.captureOutputStatus()
	model.renderTerminal(view)
	if model.bottom.heightStyle != "" {
		view.Attr("bottom-panel", "style", model.bottom.heightStyle)
	}
	model.syncProblems()
	p := &model.bottom
	view.Text("status-problem-count", p.count)
	for _, ref := range [...]string{"status-problems", "status-output", "panel-close", "output-clear"} {
		view.Event(ref, htmlui.EventClick)
	}
	view.Event("output-source", htmlui.EventChange)
	view.Event("output-filter", htmlui.EventInput)
	problems, out, source := p.active == pencodePanelProblems, p.active == pencodePanelOutput, p.active == pencodePanelSourceControl
	view.Attr("status-problems", "aria-expanded", boolAttribute(problems))
	view.Attr("status-output", "aria-expanded", boolAttribute(out))
	setPanelVisible(view, "bottom-panel", p.active != pencodePanelClosed)
	setPanelVisible(view, "problems-pane", problems)
	setPanelVisible(view, "output-pane", out)
	setPanelVisible(view, "output-tools", out)
	setPanelVisible(view, "source-pane", source)
	setPanelVisible(view, "source-tools", source)
	setPanelVisible(view, "problems-empty", len(p.problems) == 0)
	if problems {
		if !p.problemGroupsInitialized && len(p.problemGroups) > 0 {
			p.problemGroups[0].expanded = true
			p.problemGroupsInitialized = true
		}
		for i, group := range p.problemGroups {
			htmlui.AppendComponentChild(view, "problem-list", uint64(i+1), definition, pencodeProblemGroupProps{Model: model, Theme: model.workbenchTheme, Icons: model.fileIcons, Group: *group}, struct{}{})
		}
	}
	if out {
		model.syncOutput()
		view.Keyed("output-source", p.options)
		view.Attr("output-filter", "value", p.filter)
		view.Text("output-content", p.outputText)
	}
}

func setPanelVisible(view *htmlui.View, ref string, visible bool) {
	if visible {
		view.RemoveAttr(ref, "hidden")
	} else {
		view.Attr(ref, "hidden", "")
	}
}

func (model *workspaceModel) syncOutput() {
	p := &model.bottom
	store := model.outputStore()
	store.Channel("pencode")
	p.channels = store.Channels(p.channels)
	if p.source == nil {
		p.source = store.Channel("pencode")
	}
	if len(p.options) != len(p.channels) {
		p.options = p.options[:0]
		for i, c := range p.channels {
			p.options = append(p.options, appearanceOption(uint64(i+1), c.Name, c.Name, c == p.source))
		}
	}
	text, revision := p.source.Snapshot()
	if p.cachedSource == p.source && p.outputRevision == revision && p.cachedFilter == p.filter {
		return
	}
	p.cachedSource, p.outputRevision, p.cachedFilter = p.source, revision, p.filter
	p.outputText = text
	if p.filter != "" {
		var filtered strings.Builder
		for line := range strings.SplitAfterSeq(text, "\n") {
			if strings.Contains(line, p.filter) {
				filtered.WriteString(line)
			}
		}
		p.outputText = filtered.String()
	}
}

func (model *workspaceModel) handleBottomPanelEvent(world *module.Content, ref, value string) bool {
	if model.portEvent(ref, value) {
		return true
	}
	if model.handleTerminalContextEvent(world, ref) || model.terminalEvent(ref) {
		return true
	}
	p := &model.bottom
	switch ref {
	case "status-problems":
		if p.active == pencodePanelProblems {
			p.active = pencodePanelClosed
		} else {
			model.showLanguageProblems()
		}
	case "status-output":
		if p.active == pencodePanelOutput {
			p.active = pencodePanelClosed
		} else {
			p.active = pencodePanelOutput
		}
	case "panel-close":
		if p.active == pencodePanelExtension && model.actions.ui.panel != nil {
			model.closeExtensionSurface(model.actions.ui.panel, "button", true)
		}
		p.active = pencodePanelClosed
		if world != nil {
			world.Focus(model.editor.control)
		}
	case "output-source":
		for _, c := range p.channels {
			if c.Name == value {
				p.source = c
				p.options = p.options[:0]
				break
			}
		}
	case "output-filter":
		p.filter = value
	case "output-clear":
		if p.source != nil {
			p.source.Clear()
		}
	default:
		return false
	}
	return true
}
