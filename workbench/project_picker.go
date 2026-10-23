package workbench

import (
	"context"
	"strings"
	"time"

	"github.com/diiyw/onix/module"
	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/configuration"
	"github.com/pencode-labs/pencode/connection"
	htmlui "github.com/diiyw/onix/ui/html"
)

type pencodeProjectDirectory struct {
	backend       *client.Client
	configuration *configuration.Snapshot
	path, parent  string
	names         []string
	err           error
}

type pencodeProjectPicker struct {
	configurationBackend *client.Client
	configuration        *configuration.Snapshot
	profile              connection.Profile
	open, busy           bool
	connectionOpen       bool
	path, input          string
	parent               string
	message              string
	failed               bool
	names                []string
	results              chan pencodeProjectDirectory
	cancel               context.CancelFunc
	done                 chan struct{}
}

func (p *pencodeProjectPicker) stop() {
	if p.cancel != nil {
		p.cancel()
		<-p.done
		p.cancel, p.done = nil, nil
		select {
		case <-p.results:
		default:
		}
	}
	p.busy = false
}

func (model *workspaceModel) browseProjectDirectory(directory string) {
	p := &model.session.connection.picker
	if !p.open || p.busy {
		return
	}
	if !model.connectionConnected(p.profile) {
		p.message, p.failed = "SSH 已断开，请重新连接后选择项目", true
		return
	}
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return
	}
	if p.results == nil {
		p.results = make(chan pencodeProjectDirectory, 1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	p.cancel, p.done, p.busy = cancel, make(chan struct{}), true
	p.message, p.failed = "正在读取目录…", false
	backend := model.session.connection.links[p.profile.SSH].backend
	results, done, wake := p.results, p.done, model.backgroundWake()
	readConfiguration := model.backend != backend && p.configurationBackend != backend
	go func() {
		defer close(done)
		current, parent, names, err := backend.BrowseDirectory(ctx, directory)
		var snapshot *configuration.Snapshot
		if err == nil && readConfiguration {
			value, readErr := backend.ReadConfiguration(ctx)
			err, snapshot = readErr, &value
		}
		results <- pencodeProjectDirectory{backend: backend, configuration: snapshot, path: current, parent: parent, names: names, err: err}
		if wake != nil {
			wake()
		}
	}()
}

func (model *workspaceModel) pollProjectPicker() bool {
	p := &model.session.connection.picker
	select {
	case result := <-p.results:
		<-p.done
		p.cancel()
		p.cancel, p.done, p.busy = nil, nil, false
		if result.err != nil {
			p.message, p.failed = "读取目录失败："+result.err.Error(), true
			return true
		}
		p.path, p.input, p.parent, p.names = result.path, result.path, result.parent, result.names
		if result.configuration != nil {
			p.configuration = result.configuration
			p.configurationBackend = result.backend
		}
		p.message, p.failed = "选择项目目录后，点击“打开此目录”", false
		if len(p.names) == 0 {
			p.message = "此目录没有子文件夹，可以直接打开此目录"
		}
		return true
	default:
		return false
	}
}

type pencodeProjectDirectoryProps struct {
	Model *workspaceModel
	Name  string
}

func newProjectDirectoryDefinition(content *module.Content) (*htmlui.Definition[pencodeProjectDirectoryProps, struct{}], error) {
	return module.ContentDefine[pencodeProjectDirectoryProps, struct{}](content,
		htmlui.NewTemplate(`<button data-ref="directory" class="pencode-project-directory" type="button"><span aria-hidden="true">›</span><span data-ref="name"></span></button>`), "",
		module.ContentBehavior[pencodeProjectDirectoryProps, struct{}]{
			Render: func(c module.ContentRenderContext[pencodeProjectDirectoryProps, struct{}], view *htmlui.View) {
				view.Text("name", c.Props.Name)
				view.Event("directory", htmlui.EventClick)
			},
			HandleEvent: func(c *module.ContentEventContext[pencodeProjectDirectoryProps, struct{}]) {
				model := c.Props.Model
				p := &model.session.connection.picker
				model.browseProjectDirectory(strings.TrimSuffix(p.path, "/") + "/" + c.Props.Name)
				markPencodeRootDirtyByModel(c.World, model)
			},
		})
}

func (model *workspaceModel) renderProjectPicker(view *htmlui.View, definition *htmlui.Definition[pencodeProjectDirectoryProps, struct{}]) {
	if model.session == nil || model.session.connection == nil || !model.session.connection.picker.open {
		view.Attr("connection-project-overlay", "hidden", "")
		return
	}
	p := &model.session.connection.picker
	view.RemoveAttr("connection-project-overlay", "hidden")
	view.Attr("content", "disabled", "")
	view.Attr("connection-project-close-icon", "src", model.themedIcon("icons/close.svg", "icon.foreground"))
	view.Attr("connection-project-up-icon", "src", model.themedIcon("icons/arrow-up.svg", "icon.foreground"))
	view.Attr("connection-project-picker", "aria-busy", boolAttribute(p.busy))
	view.Text("connection-project-host", p.profile.Label())
	view.Attr("connection-project-path", "value", p.input)
	view.Text("connection-project-feedback", p.message)
	class := "pencode-connection-feedback"
	if p.failed {
		class += " error"
	}
	view.Attr("connection-project-feedback", "class", class)
	view.Event("connection-project-path", htmlui.EventInput)
	for _, ref := range [...]string{"connection-project-close", "connection-project-cancel", "connection-project-up", "connection-project-open"} {
		view.Event(ref, htmlui.EventClick)
		disabled := p.busy && (ref == "connection-project-up" || ref == "connection-project-open")
		disabled = disabled || ref == "connection-project-up" && (p.path == "" || p.parent == p.path) || ref == "connection-project-open" && (p.path == "" || p.input != p.path || p.failed)
		if disabled {
			view.Attr(ref, "disabled", "")
		} else {
			view.RemoveAttr(ref, "disabled")
		}
	}
	for _, name := range p.names {
		htmlui.AppendComponentChild(view, "connection-project-list", componentKey(p.path+"/"+name), definition, pencodeProjectDirectoryProps{Model: model, Name: name}, struct{}{})
	}
}

func (model *workspaceModel) projectPickerEvent(ref, value string) {
	c := model.session.connection
	p := &c.picker
	if !p.open {
		return
	}
	switch ref {
	case "connection-project-path":
		p.input = value
	case "connection-project-close", "connection-project-cancel":
		p.stop()
		p.open, c.open = false, p.connectionOpen
	case "connection-project-go":
		model.browseProjectDirectory(p.input)
	case "connection-project-up":
		if p.parent != "" {
			model.browseProjectDirectory(p.parent)
		}
	case "connection-project-open":
		if p.busy || p.path == "" || p.input != p.path || p.failed {
			return
		}
		if !model.connectionConnected(p.profile) {
			p.message, p.failed = "SSH 已断开，请重新连接后选择项目", true
			return
		}
		backend := c.links[p.profile.SSH].backend
		if model.session.backend != backend {
			if len(model.projects) != 0 {
				p.message, p.failed = "请先关闭当前项目，再打开其他设备的项目", true
				return
			}
			if p.configuration == nil || p.configurationBackend != backend {
				p.message, p.failed = "尚未读取远程配置，请重新选择目录", true
				return
			}
			if c.watchCancel != nil {
				c.watchCancel()
				c.watchCancel = nil
			}
			model.retireConnection(false)
			model.acceptConnection(pencodeConnectionResult{backend: backend, profile: p.profile, configuration: *p.configuration})
		}
		if err := model.loadProject(p.path); err != nil {
			p.message, p.failed, c.open = err.Error(), true, true
			return
		}
		p.open, c.open = false, false
	}
}
