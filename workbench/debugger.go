package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/dap"
	"github.com/pencode-labs/pencode/debugger"
	"github.com/pencode-labs/pencode/protocol"
	"github.com/pencode-labs/pencode/terminal"
)

var pencodeDebugEmptyOptions = [...]htmlui.VNode{appearanceOption(0, "", "暂无启动器", true)}

type pencodeBreakpoint struct{ offset, line int }
type pencodeDebugEvent struct {
	project           *pencodeProject
	configs           []debugger.Config
	err               error
	ctx               context.Context
	edit              bool
	written, baseline string
	stream            *client.Process
	claim             func() bool
	title             string
}
type pencodeDebugState struct {
	session                     *debugger.Session
	project, configProject      *pencodeProject
	backend                     *client.Client
	view                        debugger.Snapshot
	configs                     []debugger.Config
	options                     []htmlui.VNode
	selected                    int
	loading, startPending       bool
	adding                      bool
	templateIndex               int
	templateStyle               string
	threadsExpanded             bool
	events                      chan pencodeDebugEvent
	cancel                      context.CancelFunc
	expanded                    map[int]bool
	expression, message, output string
	outputRevision              uint64
	navigationEpoch             uint64
	navigationFrame             int
	rows                        []pencodeDebugRow
	rowRevision                 uint64
	breakpointProject           *pencodeProject
	breakpointRevision          uint64
	breakpointNames             []string
}
type pencodeDebugRow struct {
	ref, slot, label, detail       string
	depth                          int
	expandable, expanded, selected bool
}
type pencodeDebugRowProps struct {
	Model *workspaceModel
	Theme *pencodeTheme
	Row   pencodeDebugRow
}

func newDebugRowDefinition(content *module.Content) (*htmlui.Definition[pencodeDebugRowProps, struct{}], error) {
	return content.Define[pencodeDebugRowProps, struct{}](htmlui.NewTemplate(`<button data-ref="row" class="pencode-debug-row" type="button"><span data-ref="toggle" class="pencode-debug-toggle"></span><span data-ref="label" class="pencode-debug-label"></span><span data-ref="detail" class="pencode-debug-detail"></span></button>`), "", module.ContentBehavior[pencodeDebugRowProps, struct{}]{
		Render: func(c module.ContentRenderContext[pencodeDebugRowProps, struct{}], view *htmlui.View) {
			r := c.Props.Row
			if r.slot == "debug-template-menu" {
				view.Attr("row", "id", r.ref)
				view.Attr("row", "role", "menuitem")
				view.Attr("row", "tabindex", "-1")
				class := ""
				if r.selected {
					class = "active"
				}
				view.Attr("row", "class", class)
				view.Attr("toggle", "hidden", "")
				view.Attr("detail", "hidden", "")
				view.Text("label", r.label)
				view.Event("row", htmlui.EventClick)
				return
			}
			view.Attr("row", "id", r.ref)
			view.Attr("row", "title", r.label+" "+r.detail)
			view.Attr("row", "style", "padding-left:"+strconv.Itoa(8+r.depth*12)+"px")
			class := "pencode-debug-row"
			if r.selected {
				class += " selected"
			}
			view.Attr("row", "class", class)
			view.Event("row", htmlui.EventClick)
			view.Text("label", r.label)
			view.Text("detail", r.detail)
			arrow := ""
			if r.expandable {
				arrow = "▸"
				if r.expanded {
					arrow = "▾"
				}
				view.Attr("row", "aria-expanded", boolAttribute(r.expanded))
			} else {
				view.RemoveAttr("row", "aria-expanded")
			}
			view.Text("toggle", arrow)
		},
		HandleEvent: func(c *module.ContentEventContext[pencodeDebugRowProps, struct{}]) {
			if c.Ref == "row" {
				c.Props.Model.debugEvent(c.World, c.Props.Row.ref, "")
				markPencodeRootDirtyByModel(c.World, c.Props.Model)
			}
		},
	})
}

func (m *workspaceModel) debugEvents() chan pencodeDebugEvent {
	if m.debug.events == nil {
		m.debug.events = make(chan pencodeDebugEvent, 8)
	}
	return m.debug.events
}

func (m *workspaceModel) loadDebugConfigs() {
	m.readDebugConfigs(false, "")
}

func (m *workspaceModel) readDebugConfigs(edit bool, template string) {
	u := &m.debug
	p := m.activeProjectState()
	if u.loading && u.configProject == p {
		return
	}
	if u.cancel != nil {
		u.cancel()
	}
	if p == nil || p.backend == nil {
		u.configProject, u.configs, u.options = p, nil, nil
		u.loading, u.startPending, u.message = false, false, ""
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	u.cancel, u.loading, u.configProject = cancel, true, p
	u.configs, u.options, u.message = nil, nil, ""
	u.adding = false
	root := p.Path
	events, wake, backend := m.debugEvents(), m.backgroundWake(), p.backend
	workspace := p.workspace.ID
	go func() {
		request, stop := context.WithTimeout(ctx, 10*time.Second)
		defer stop()
		data, err := backend.WorkFile(request, root, pencodeDebugConfigPath)
		baseline, written := string(data), ""
		if edit && err == nil && (data == nil || template != "") {
			content := "[]\n"
			if template != "" {
				content, err = appendDebugTemplate(data, template)
			}
			if err == nil {
				// An existing directory is fine; SaveFile reports any failure to
				// create the parent or a conflicting change to the config file.
				_ = backend.Create(request, workspace, ".pencode", true)
				err = backend.SaveFile(request, root, pencodeDebugConfigPath, baseline, content)
				if err == nil {
					data, written = []byte(content), content
				}
			}
		}
		var configs []debugger.Config
		if err == nil && data != nil {
			configs, err = debugger.ParseConfigs(data, root)
		}
		event := pencodeDebugEvent{project: p, configs: configs, err: err, ctx: ctx, edit: edit && data != nil, written: written, baseline: baseline}
		select {
		case <-ctx.Done():
			return
		case events <- event:
		}
		if wake != nil {
			wake()
		}
	}()
}

func (m *workspaceModel) startDebug() {
	u := &m.debug
	m.bottom.active = pencodePanelDebug
	if u.session != nil && u.view.State < debugger.Ended {
		return
	}
	if !m.backendConnected() {
		u.message = "工作区服务未连接"
		return
	}
	if u.configProject != m.activeProjectState() || u.loading {
		u.startPending = true
		m.loadDebugConfigs()
		return
	}
	if len(u.configs) == 0 {
		return
	}
	p := u.configProject
	for _, f := range p.Files {
		if f.Dirty {
			u.message = "请先保存项目中的更改，再启动调试"
			return
		}
	}
	if u.session != nil {
		u.session.Close()
	}
	config := u.configs[min(u.selected, len(u.configs)-1)]
	filename := ""
	if file := m.activeFile(); file != nil {
		filename = path.Join(strings.ReplaceAll(p.Path, "\\", "/"), file.Path)
	}
	config, err := config.ResolveFile(filename)
	if err != nil {
		u.message = err.Error()
		return
	}
	u.project, u.backend, u.message = p, p.backend, ""
	u.view = debugger.Snapshot{}
	u.expanded, u.rows = nil, nil
	u.navigationEpoch, u.navigationFrame, u.rowRevision = 0, 0, 0
	channel := m.outputStore().Channel("Debugger")
	channel.Clear()
	backend, root, events, wake := p.backend, p.Path, m.debugEvents(), m.backgroundWake()
	open := func(ctx context.Context) (io.ReadWriteCloser, error) {
		stream, err := backend.OpenDebugAdapter(ctx, root, config.Adapter, config.Command, config.Address, config.Args)
		if err == nil {
			go func() { _, _ = io.Copy(channel, stream.Stderr()) }()
		}
		return stream, err
	}
	handler := func(ctx context.Context, command string, payload json.RawMessage) (any, error) {
		if command != "runInTerminal" {
			return nil, fmt.Errorf("unsupported adapter request: %s", command)
		}
		var args dap.RunInTerminalArguments
		if err := json.Unmarshal(payload, &args); err != nil {
			return nil, err
		}
		if args.Kind == "external" || args.ArgsCanBeInterpretedByShell {
			return nil, fmt.Errorf("only integrated terminals with direct argv execution are supported")
		}
		request, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		stream, pid, err := backend.OpenDebugTerminal(request, root, protocol.TerminalCommand{Directory: args.Cwd, Args: args.Args, Env: args.Env})
		if err != nil {
			return nil, err
		}
		claim := context.AfterFunc(ctx, func() { _ = stream.Close() })
		select {
		case <-ctx.Done():
			_ = stream.Close()
			return nil, ctx.Err()
		case events <- pencodeDebugEvent{project: p, stream: stream, title: args.Title, claim: claim}:
		}
		if wake != nil {
			wake()
		}
		return struct {
			ProcessID int `json:"processId"`
		}{pid}, nil
	}
	u.session = debugger.Start(context.Background(), config, m.debugBreakpointSnapshot(p), open, handler, channel, wake)
	u.view = u.session.Snapshot()
}

func (m *workspaceModel) closeDebugger() {
	u := &m.debug
	if u.cancel != nil {
		u.cancel()
		u.cancel = nil
	}
	if u.session != nil {
		u.session.Close()
		u.session = nil
	}
	if u.events != nil {
		for {
			select {
			case event := <-u.events:
				if event.stream != nil {
					_ = event.stream.Close()
				}
			default:
				goto drained
			}
		}
	}
drained:
	u.events, u.configProject, u.configs, u.options, u.project, u.backend = nil, nil, nil, nil, nil, nil
	u.loading, u.startPending, u.adding = false, false, false
	u.view = debugger.Snapshot{State: debugger.Ended}
	u.rows, u.expanded = nil, nil
}

func (m *workspaceModel) pollDebugger(world *module.Content) bool {
	u := &m.debug
	changed := false
	if m.bottom.active == pencodePanelDebug && u.configProject != m.activeProjectState() {
		u.startPending = false
		m.loadDebugConfigs()
		changed = true
	}
	for u.events != nil {
		select {
		case event := <-u.events:
			changed = true
			if event.stream != nil {
				if !event.claim() || u.session == nil || event.project != u.project {
					_ = event.stream.Close()
					continue
				}
				t := m.terminalState()
				t.nextID++
				id := strconv.FormatUint(t.nextID, 10)
				label := event.title
				if label == "" {
					label = "Debug Terminal"
				}
				item := &pencodeTerminal{id: id, root: event.project.Path, label: label, selectRef: "terminal-tab:" + id, closeRef: "terminal-close:" + id, screen: terminal.NewScreen(80, 12)}
				stream := event.stream
				item.session = terminal.Attach(80, 12, m.backgroundWake(), func() (terminal.Process, error) { return stream, nil })
				t.items = append(t.items, item)
				t.active = item
				continue
			}
			if event.project != u.configProject || event.ctx != nil && event.ctx.Err() != nil {
				continue
			}
			u.loading = false
			u.message = ""
			if event.edit && event.project == m.activeProjectState() {
				if err := m.requestFileOpen(event.project, pencodeDebugConfigPath, false, true, nil); err != nil {
					u.message = err.Error()
				}
			}
			// A file may have been opened while the worker was writing. Retain
			// edits made in the meantime, otherwise update its saved snapshot.
			if event.written != "" && event.project == m.activeProjectState() {
				if f := event.project.Files[pencodeDebugConfigPath]; f != nil && !f.Dirty && f.Content == event.baseline {
					f.closeDocument()
					m.applyFileContent(f, event.project, event.written, true)
					f.savedContent, f.Dirty = event.written, false
					f.saveRevision++
				}
			}
			if event.err != nil {
				u.message = event.err.Error()
				u.startPending = false
				continue
			}
			u.configs, u.selected = event.configs, 0
			u.options = u.options[:0]
			for i, config := range u.configs {
				u.options = append(u.options, appearanceOption(uint64(i+1), strconv.Itoa(i), config.Label, i == 0))
			}
			if u.startPending {
				u.startPending = false
				if event.project == m.activeProjectState() {
					m.startDebug()
				}
			}
		default:
			goto eventsDrained
		}
	}
eventsDrained:
	if u.session == nil {
		return changed
	}
	if u.backend != m.backend || !m.backendConnected() {
		m.closeDebugger()
		u.message = "调试连接已断开"
		return true
	}
	view := u.session.Snapshot()
	if view.Revision != u.view.Revision {
		if view.Epoch != u.view.Epoch {
			u.expanded = nil
		}
		u.view, u.rowRevision, changed = view, 0, true
		if view.State == debugger.Stopped && (u.navigationEpoch != view.Epoch || u.navigationFrame != view.FrameID) {
			for _, frame := range view.Frames {
				if frame.ID == view.FrameID && frame.Source.Path != "" {
					m.navigateDebugFrame(frame)
					u.navigationEpoch, u.navigationFrame = view.Epoch, view.FrameID
					m.bottom.active = pencodePanelDebug
					break
				}
			}
		}
	}
	text, revision := u.session.Output.Snapshot()
	if revision != u.outputRevision {
		u.output, u.outputRevision = text, revision
		changed = changed || m.bottom.active == pencodePanelDebug
	}
	return changed
}

func (m *workspaceModel) navigateDebugFrame(frame dap.StackFrame) {
	p := m.debug.project
	if p == nil {
		return
	}
	filename := strings.ReplaceAll(frame.Source.Path, "\\", "/")
	root := strings.TrimSuffix(strings.ReplaceAll(p.Path, "\\", "/"), "/")
	if !strings.HasPrefix(filename, root+"/") {
		return
	}
	m.activateProject(p)
	if err := m.requestFileOpen(p, strings.TrimPrefix(filename, root+"/"), false, true, &fileNavigation{line: max(1, frame.Line)}); err != nil {
		m.debug.message = err.Error()
	}
}

func (m *workspaceModel) debugEvent(world *module.Content, ref, value string) bool {
	u := &m.debug
	switch ref {
	case "status-debug":
		if m.bottom.active == pencodePanelDebug {
			m.bottom.active = pencodePanelClosed
		} else {
			m.bottom.active = pencodePanelDebug
			if u.configProject != m.activeProjectState() || len(u.configs) == 0 {
				m.loadDebugConfigs()
			}
		}
	case "debug-start":
		m.startDebug()
	case "debug-reload":
		m.loadDebugConfigs()
	case "debug-add":
		m.toggleDebugTemplateMenu(world)
	case "debug-config":
		u.selected, _ = strconv.Atoi(value)
		u.selected = min(max(0, u.selected), max(0, len(u.configs)-1))
		for i, config := range u.configs {
			u.options[i] = appearanceOption(uint64(i+1), strconv.Itoa(i), config.Label, i == u.selected)
		}
	case "debug-stop":
		if u.session != nil {
			u.session.Stop()
		}
	case "debug-continue", "debug-next", "debug-stepIn", "debug-stepOut":
		if u.session != nil {
			command := strings.TrimPrefix(ref, "debug-")
			if command == "continue" && u.view.State == debugger.Running {
				command = "pause"
			}
			u.session.Control(command)
		}
	case "debug-expression":
		u.expression = value
	case "debug-toggle-threads":
		u.threadsExpanded = !u.threadsExpanded
		u.rowRevision = 0
	case "debug-clear":
		u.output = ""
		if u.session != nil {
			u.session.ClearConsole()
			u.view = u.session.Snapshot()
			if u.session.Output != nil {
				u.output, u.outputRevision = u.session.Output.Snapshot()
			}
		}
		u.rowRevision = 0
	case "debug-evaluate":
		if u.session != nil && strings.TrimSpace(u.expression) != "" {
			u.session.Evaluate(u.expression)
		}
	default:
		if value, ok := strings.CutPrefix(ref, "debug-template:"); ok {
			m.chooseDebugTemplate(value)
			return true
		}
		if filename, ok := strings.CutPrefix(ref, "debug-bp:"); ok {
			index := strings.LastIndexByte(filename, ':')
			if index < 0 || u.breakpointProject == nil {
				return true
			}
			line, _ := strconv.Atoi(filename[index+1:])
			m.activateProject(u.breakpointProject)
			if err := m.requestFileOpen(u.breakpointProject, filename[:index], false, true, &fileNavigation{line: line}); err != nil {
				u.message = err.Error()
			}
			return true
		}
		kind, id, ok := strings.Cut(strings.TrimPrefix(ref, "debug-row:"), ":")
		if !ok || !strings.HasPrefix(ref, "debug-row:") || u.session == nil {
			return false
		}
		id, _, _ = strings.Cut(id, ":")
		n, _ := strconv.Atoi(id)
		switch kind {
		case "thread":
			u.session.SelectThread(n)
		case "frame":
			u.session.SelectFrame(n)
		case "variable", "scope", "evaluation":
			if n <= 0 {
				return true
			}
			if u.expanded == nil {
				u.expanded = make(map[int]bool)
			}
			if expanded, exists := u.expanded[n]; exists {
				u.expanded[n] = !expanded
			} else {
				u.expanded[n] = kind != "scope"
			}
			if u.expanded[n] {
				u.session.Expand(n)
			}
			u.rowRevision = 0
		default:
			return false
		}
	}
	return true
}

func (m *workspaceModel) debugShortcut(world *module.Content, state *input.State) bool {
	if m.screen != pencodeWorkspace || m.settingsOpen || m.quickOpen || m.findOpen || m.market != nil && m.market.open {
		return false
	}
	switch {
	case m.commandMatches("debug-stop", state, runtime.GOOS):
		return m.debugEvent(world, "debug-stop", "")
	case m.commandMatches("debug-start", state, runtime.GOOS):
		if m.debug.session != nil && m.debug.view.State < debugger.Ended {
			return m.debugEvent(world, "debug-continue", "")
		}
		return m.debugEvent(world, "debug-start", "")
	case m.commandMatches("debug-breakpoint", state, runtime.GOOS):
		m.toggleDebugBreakpoint(world, 0)
		return true
	case m.commandMatches("debug-next", state, runtime.GOOS) && m.debug.view.State == debugger.Stopped:
		return m.debugEvent(world, "debug-next", "")
	case m.commandMatches("debug-stepIn", state, runtime.GOOS) && m.debug.session != nil && m.debug.view.State == debugger.Stopped:
		return m.debugEvent(world, "debug-stepIn", "")
	case m.commandMatches("debug-stepOut", state, runtime.GOOS) && m.debug.session != nil && m.debug.view.State == debugger.Stopped:
		return m.debugEvent(world, "debug-stepOut", "")
	case m.commandMatches("debug-evaluate", state, runtime.GOOS) && m.bottom.active == pencodePanelDebug && pencodeControlFocused(world, m.surface, "debug-expression"):
		return m.debugEvent(world, "debug-evaluate", "")
	}
	return false
}

func (m *workspaceModel) debugBreakpointSnapshot(p *pencodeProject) map[string][]dap.SourceBreakpoint {
	result := make(map[string][]dap.SourceBreakpoint, len(p.debugBreakpoints))
	for filename, bps := range p.debugBreakpoints {
		list := make([]dap.SourceBreakpoint, 0, len(bps))
		for _, bp := range bps {
			list = append(list, dap.SourceBreakpoint{Line: bp.line})
		}
		result[path.Join(p.Path, filename)] = list
	}
	return result
}

func (m *workspaceModel) syncDebugBreakpoints(p *pencodeProject, file *openFile) {
	if p == nil || file == nil || m.debug.session == nil || m.debug.project != p || file.Dirty {
		return
	}
	list := make([]dap.SourceBreakpoint, 0, len(p.debugBreakpoints[file.Path]))
	for _, bp := range p.debugBreakpoints[file.Path] {
		list = append(list, dap.SourceBreakpoint{Line: bp.line})
	}
	m.debug.session.SetBreakpoints(path.Join(p.Path, file.Path), list)
}

func (m *workspaceModel) toggleDebugBreakpoint(world *module.Content, line int) {
	p, f := m.activeProjectState(), m.activeFile()
	if p == nil || f == nil || !m.textEditorActive() {
		return
	}
	if line <= 0 {
		caret := 0
		if world != nil {
			if field, ok := world.Get[engineui.TextField](m.editor.control); ok {
				caret = f.textBuffer(f.Content).ByteOffset(field.Caret)
			}
		}
		line = 1 + strings.Count(f.Content[:min(caret, len(f.Content))], "\n")
	}
	offset := 0
	for n := 1; n < line; n++ {
		end := strings.IndexByte(f.Content[offset:], '\n')
		if end < 0 {
			return
		}
		offset += end + 1
	}
	if p.debugBreakpoints == nil {
		p.debugBreakpoints = make(map[string][]pencodeBreakpoint)
	}
	list := p.debugBreakpoints[f.Path]
	index, exists := slices.BinarySearchFunc(list, pencodeBreakpoint{offset: offset}, func(a, b pencodeBreakpoint) int { return a.offset - b.offset })
	if exists {
		list = slices.Delete(list, index, index+1)
	} else {
		list = slices.Insert(list, index, pencodeBreakpoint{offset: offset, line: line})
	}
	p.debugBreakpoints[f.Path] = list
	p.debugBreakpointRevision++
	m.syncDebugBreakpoints(p, f)
}

func (m *workspaceModel) rebaseDebugBreakpoints(p *pencodeProject, f *openFile, value string, start, oldEnd, newEnd int) {
	if p == nil {
		return
	}
	list := p.debugBreakpoints[f.Path]
	if len(list) == 0 {
		return
	}
	for i := range list {
		offset := list[i].offset
		if offset >= oldEnd {
			offset += newEnd - oldEnd
		} else if offset >= start {
			offset = start
		}
		offset = min(max(offset, 0), len(value))
		offset = strings.LastIndexByte(value[:offset], '\n') + 1
		list[i].offset = offset
		list[i].line = 1 + strings.Count(value[:offset], "\n")
	}
	if len(list) > 1 {
		list = slices.Compact(list)
	}
	if len(list) != 0 {
		p.debugBreakpoints[f.Path] = list
	}
	p.debugBreakpointRevision++
}

func (m *workspaceModel) relocateDebugBreakpoints(source, target *pencodeProject, old, next string) {
	for name, bps := range source.debugBreakpoints {
		if !pencodeResourceContains(old, name) {
			continue
		}
		delete(source.debugBreakpoints, name)
		source.debugBreakpointRevision++
		if m.debug.session != nil && m.debug.project == source {
			m.debug.session.SetBreakpoints(path.Join(source.Path, name), nil)
		}
		if target == nil {
			continue
		}
		updated := next + strings.TrimPrefix(name, old)
		if target.debugBreakpoints == nil {
			target.debugBreakpoints = make(map[string][]pencodeBreakpoint)
		}
		target.debugBreakpoints[updated] = bps
		target.debugBreakpointRevision++
		if m.debug.session != nil && m.debug.project == target {
			list := make([]dap.SourceBreakpoint, 0, len(bps))
			for _, bp := range bps {
				list = append(list, dap.SourceBreakpoint{Line: bp.line})
			}
			m.debug.session.SetBreakpoints(path.Join(target.Path, updated), list)
		}
	}
}

func (m *workspaceModel) debugRows() {
	u := &m.debug
	p := m.activeProjectState()
	if u.session != nil {
		p = u.project
	}
	var revision uint64
	if p != nil {
		revision = p.debugBreakpointRevision
	}
	if u.rowRevision == u.view.Revision && (u.rowRevision != 0 || u.rows != nil) && u.breakpointProject == p && u.breakpointRevision == revision {
		return
	}
	u.rows = u.rows[:0]
	u.breakpointProject, u.breakpointRevision = p, revision
	if p != nil {
		u.breakpointNames = u.breakpointNames[:0]
		for name := range p.debugBreakpoints {
			u.breakpointNames = append(u.breakpointNames, name)
		}
		slices.Sort(u.breakpointNames)
		for _, name := range u.breakpointNames {
			verified := u.view.Breakpoints[path.Join(p.Path, name)]
			for i, bp := range p.debugBreakpoints[name] {
				detail := "第 " + strconv.Itoa(bp.line) + " 行"
				if u.session != nil && u.view.State < debugger.Ended {
					if i < len(verified) && verified[i].Verified {
						detail += " · 已验证"
					} else {
						detail += " · 待验证"
						if i < len(verified) && verified[i].Message != "" {
							detail += " · " + verified[i].Message
						}
					}
				}
				u.rows = append(u.rows, pencodeDebugRow{ref: "debug-bp:" + name + ":" + strconv.Itoa(bp.line), slot: "debug-breakpoints", label: name, detail: detail})
			}
		}
	}
	if u.threadsExpanded {
		for _, thread := range u.view.Threads {
			u.rows = append(u.rows, pencodeDebugRow{ref: "debug-row:thread:" + strconv.Itoa(thread.ID), slot: "debug-threads", label: thread.Name, selected: thread.ID == u.view.ThreadID})
		}
	}
	for _, frame := range u.view.Frames {
		u.rows = append(u.rows, pencodeDebugRow{ref: "debug-row:frame:" + strconv.Itoa(frame.ID), slot: "debug-frames", label: frame.Name, detail: path.Base(frame.Source.Path) + ":" + strconv.Itoa(frame.Line), selected: frame.ID == u.view.FrameID})
	}
	for index, scope := range u.view.Scopes {
		expanded, exists := u.expanded[scope.VariablesReference]
		if !exists {
			expanded = !scope.Expensive
		}
		u.rows = append(u.rows, pencodeDebugRow{ref: "debug-row:scope:" + strconv.Itoa(scope.VariablesReference) + ":" + strconv.Itoa(index), slot: "debug-variables", label: scope.Name, expandable: scope.VariablesReference > 0, expanded: expanded})
		if expanded {
			m.debugVariableRows(scope.VariablesReference, 1, strconv.Itoa(index), "debug-variables")
		}
	}
	for index, result := range u.view.Evaluations {
		expanded := u.expanded[result.VariablesReference]
		key := "eval:" + strconv.Itoa(index)
		ref := "debug-row:evaluation:" + strconv.Itoa(result.VariablesReference) + ":" + key
		u.rows = append(u.rows, pencodeDebugRow{ref: ref, slot: "debug-evaluations", label: "> " + result.Name, detail: result.Value, expandable: result.VariablesReference > 0, expanded: expanded})
		if expanded {
			m.debugVariableRows(result.VariablesReference, 1, key, "debug-evaluations")
		}
	}
	u.rowRevision = u.view.Revision
}

func (m *workspaceModel) debugVariableRows(reference, depth int, parent, slot string) {
	u := &m.debug
	if depth > 12 || len(u.rows) > 2000 {
		return
	}
	for i, variable := range u.view.Variables[reference] {
		expanded := u.expanded[variable.VariablesReference]
		key := parent + ":" + strconv.Itoa(i)
		ref := "debug-row:variable:" + strconv.Itoa(variable.VariablesReference) + ":" + key
		if variable.VariablesReference == 0 {
			ref = "debug-leaf:" + key
		}
		u.rows = append(u.rows, pencodeDebugRow{ref: ref, slot: slot, label: variable.Name, detail: variable.Value, depth: depth, expandable: variable.VariablesReference > 0, expanded: expanded})
		if expanded && variable.VariablesReference != reference {
			m.debugVariableRows(variable.VariablesReference, depth+1, key, slot)
		}
	}
}

func (m *workspaceModel) renderDebugger(view *htmlui.View, definition *htmlui.Definition[pencodeDebugRowProps, struct{}]) {
	u := &m.debug
	active := m.bottom.active == pencodePanelDebug
	view.Event("status-debug", htmlui.EventClick)
	view.Attr("status-debug", "aria-expanded", boolAttribute(active))
	setPanelVisible(view, "debug-pane", active)
	setPanelVisible(view, "debug-tools", active)
	setPanelVisible(view, "debug-template-menu", active && u.adding)
	view.Attr("debug-add", "aria-expanded", boolAttribute(active && u.adding))
	if active && u.adding {
		view.Attr("debug-template-menu", "style", u.templateStyle)
		view.Attr("debug-template-menu", "aria-activedescendant", m.debugTemplateRows[u.templateIndex].ref)
		for i, row := range m.debugTemplateRows {
			row.selected = i == u.templateIndex
			view.AppendComponentChild("debug-template-menu", uint64(i+1), definition, pencodeDebugRowProps{Model: m, Theme: m.workbenchTheme, Row: row}, struct{}{})
		}
	}
	if !active {
		return
	}
	view.Attr("bottom-panel", "class", "pencode-bottom-panel pencode-debug-panel")
	for _, ref := range [...]string{"debug-start", "debug-stop", "debug-continue", "debug-next", "debug-stepIn", "debug-stepOut", "debug-evaluate", "debug-reload", "debug-add", "debug-toggle-threads", "debug-clear"} {
		view.Event(ref, htmlui.EventClick)
	}
	view.Attr("debug-toggle-threads", "aria-expanded", boolAttribute(u.threadsExpanded))
	arrow := "▸"
	if u.threadsExpanded {
		arrow = "▾"
	}
	view.Text("debug-threads-toggle", arrow)
	setPanelVisible(view, "debug-threads", u.threadsExpanded)
	view.Event("debug-config", htmlui.EventChange)
	view.Event("debug-expression", htmlui.EventInput)
	options, selected := u.options, ""
	if len(u.configs) == 0 {
		options = pencodeDebugEmptyOptions[:]
	} else {
		selected = strconv.Itoa(u.selected)
	}
	view.Keyed("debug-config", options)
	view.Attr("debug-config", "value", selected)
	view.Attr("debug-expression", "value", u.expression)
	view.Text("debug-message", u.message)
	setPanelVisible(view, "debug-message", u.message != "")
	running := u.session != nil && u.view.State < debugger.Ended
	stopped := running && u.view.State == debugger.Stopped
	setContextMenuEnabled(view, "debug-start", !running && !u.loading && len(u.configs) != 0)
	setContextMenuEnabled(view, "debug-config", !running && !u.loading && len(u.configs) != 0)
	setContextMenuEnabled(view, "debug-add", !running && !u.loading && m.backendConnected() && m.activeProjectState() != nil)
	setContextMenuEnabled(view, "debug-reload", !running && !u.loading)
	setContextMenuEnabled(view, "debug-stop", running)
	setContextMenuEnabled(view, "debug-continue", running && (stopped || u.view.State == debugger.Running))
	for _, ref := range [...]string{"debug-next", "debug-stepIn", "debug-stepOut", "debug-evaluate", "debug-expression"} {
		setContextMenuEnabled(view, ref, stopped)
	}
	label := "继续"
	icon, foreground := "icons/debug-continue.svg", "debugIcon.continueForeground"
	title := m.shortcutTitle("继续", "debug-start")
	if u.view.State == debugger.Running {
		label = "暂停"
		title = "暂停执行"
		icon, foreground = "icons/debug-pause.svg", "debugIcon.pauseForeground"
	}
	view.Attr("debug-continue", "aria-label", label)
	view.Attr("debug-continue", "title", title)
	view.Attr("debug-continue-icon", "src", m.themedIcon(icon, foreground))
	for _, command := range [...]struct{ ref, name string }{{"debug-start", "启动调试"}, {"debug-next", "单步跳过"}, {"debug-stepIn", "单步进入"}, {"debug-stepOut", "单步跳出"}, {"debug-stop", "停止 / 分离"}} {
		view.Attr(command.ref, "title", m.shortcutTitle(command.name, command.ref))
	}
	view.Text("debug-console", u.output)
	m.debugRows()
	for _, row := range u.rows {
		view.AppendComponentChild(row.slot, componentKey(row.ref), definition, pencodeDebugRowProps{Model: m, Theme: m.workbenchTheme, Row: row}, struct{}{})
	}
}
