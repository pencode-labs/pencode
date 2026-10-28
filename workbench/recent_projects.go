package workbench

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/diiyw/onix/module"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/connection"
	"github.com/pencode-labs/pencode/protocol"
)

const pencodeRecentProjectLimit = 10

type pencodeRecentProject struct {
	Path                          string             `json:"path"`
	Name                          string             `json:"name"`
	Profile                       connection.Profile `json:"profile"`
	key, description, removeLabel string
}

func (p pencodeRecentProject) same(other pencodeRecentProject) bool {
	return p.Path == other.Path && p.Profile.SameHost(other.Profile)
}

func (p *pencodeRecentProject) prepare() {
	if !p.Profile.Remote() {
		p.Profile = connection.Profile{}
	}
	if p.Name == "" {
		p.Name = filepath.Base(p.Path)
	}
	p.key, p.description = p.Profile.Namespace()+p.Path, p.Path
	if p.Profile.Remote() {
		p.description = p.Profile.Label() + " · " + p.Path
	}
	p.removeLabel = "从最近打开中移除 " + p.Name
}

type pencodeRecentProjects struct {
	file    string
	entries [pencodeRecentProjectLimit]pencodeRecentProject
	count   int
	dirty   bool
}

func loadPencodeRecentProjects(directory string) *pencodeRecentProjects {
	h := &pencodeRecentProjects{}
	if directory == "" {
		return h
	}
	file := filepath.Join(directory, "recent-projects.json")
	data, err := os.ReadFile(file)
	var entries []pencodeRecentProject
	if err == nil && json.Unmarshal(data, &entries) == nil {
		// Replay oldest first to retain the most recent unique projects.
		for i := len(entries) - 1; i >= 0; i-- {
			if entries[i].Path != "" {
				entries[i].prepare()
				h.remember(entries[i])
			}
		}
	}
	h.file, h.dirty = file, false
	return h
}

func (h *pencodeRecentProjects) remember(entry pencodeRecentProject) error {
	index := 0
	for index < h.count && !h.entries[index].same(entry) {
		index++
	}
	if index == 0 && h.count != 0 && h.entries[0] == entry && !h.dirty {
		return nil
	}
	if index == h.count {
		index = min(index, len(h.entries)-1)
		h.count = min(h.count+1, len(h.entries))
	}
	copy(h.entries[1:index+1], h.entries[:index])
	h.entries[0] = entry
	return h.save()
}

func (h *pencodeRecentProjects) remove(entry pencodeRecentProject) error {
	for i := range h.count {
		if h.entries[i].same(entry) {
			copy(h.entries[i:h.count-1], h.entries[i+1:h.count])
			h.count--
			h.entries[h.count] = pencodeRecentProject{}
			return h.save()
		}
	}
	return nil
}

func (h *pencodeRecentProjects) save() error {
	if h.file == "" {
		return nil
	}
	h.dirty = true
	data, err := json.Marshal(h.entries[:h.count])
	if err != nil {
		return err
	}
	directory := filepath.Dir(h.file)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".recent-projects-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	err = os.Rename(file.Name(), h.file)
	h.dirty = err != nil
	return err
}

func (m *workspaceModel) rememberRecentProject(workspace protocol.Workspace) {
	if m.session == nil || m.session.recentProjects == nil {
		return
	}
	entry := pencodeRecentProject{Path: workspace.Path, Name: workspace.Name}
	if c := m.session.connection; c != nil {
		entry.Profile = c.workspaceProfile
	}
	entry.prepare()
	if err := m.session.recentProjects.remember(entry); err != nil {
		m.logOutput("pencode", "保存最近打开的项目失败："+err.Error())
	}
}

type pencodeRecentProjectProps struct {
	Model *workspaceModel
	Theme *pencodeTheme
	Entry pencodeRecentProject
}

func newRecentProjectDefinition(content *module.Content) (*htmlui.Definition[pencodeRecentProjectProps, struct{}], error) {
	return module.ContentDefine[pencodeRecentProjectProps, struct{}](content,
		htmlui.NewTemplate(`<div class="pencode-recent-row" role="listitem"><button data-ref="open" class="pencode-recent-open" type="button"><img data-ref="icon" src="icons/folder-opened.svg" alt=""><span class="pencode-recent-copy"><span data-ref="name" class="pencode-recent-name"></span><span data-ref="path" class="pencode-recent-path"></span></span></button><button data-ref="remove" class="pencode-icon-button" type="button" title="从最近打开中移除"><img data-ref="remove-icon" class="pencode-close-icon" src="icons/close.svg" alt=""></button></div>`), "",
		module.ContentBehavior[pencodeRecentProjectProps, struct{}]{
			Render: func(c module.ContentRenderContext[pencodeRecentProjectProps, struct{}], view *htmlui.View) {
				m, entry := c.Props.Model, c.Props.Entry
				view.Text("name", entry.Name)
				view.Text("path", entry.description)
				view.Attr("open", "title", entry.description)
				view.Attr("remove", "aria-label", entry.removeLabel)
				icon := "icons/folder-opened.svg"
				if entry.Profile.Remote() {
					icon = "icons/remote.svg"
				}
				view.Attr("icon", "src", m.themedIcon(icon, "icon.foreground"))
				view.Attr("remove-icon", "src", m.themedIcon("icons/close.svg", "icon.foreground"))
				view.Event("open", htmlui.EventClick)
				view.Event("remove", htmlui.EventClick)
			},
			HandleEvent: func(c *module.ContentEventContext[pencodeRecentProjectProps, struct{}]) {
				m, entry := c.Props.Model, c.Props.Entry
				switch c.Ref {
				case "open":
					m.openRecentProject(entry)
				case "remove":
					if err := m.session.recentProjects.remove(entry); err != nil {
						m.status = "保存最近打开的项目失败：" + err.Error()
					} else {
						m.status = ""
					}
				}
				markPencodeRootDirtyByModel(c.World, m)
			},
		})
}

func (m *workspaceModel) renderRecentProjects(view *htmlui.View, definition *htmlui.Definition[pencodeRecentProjectProps, struct{}]) {
	if m.session == nil || m.session.recentProjects == nil || m.session.recentProjects.count == 0 {
		view.RemoveAttr("recent-projects-empty", "hidden")
		return
	}
	view.Attr("recent-projects-empty", "hidden", "")
	h := m.session.recentProjects
	for _, entry := range h.entries[:h.count] {
		htmlui.AppendComponentChild(view, "recent-projects", componentKey(entry.key), definition,
			pencodeRecentProjectProps{Model: m, Theme: m.workbenchTheme, Entry: entry}, struct{}{})
	}
}

func (m *workspaceModel) openRecentProject(entry pencodeRecentProject) {
	c := m.session.connection
	if c != nil && c.busy {
		m.status = "正在连接，请稍后打开项目"
		return
	}
	if entry.Profile.Remote() {
		if c == nil {
			m.status = "远程连接不可用"
			return
		}
		// Saved credentials may have changed since this project was last opened.
		for _, profile := range c.profiles {
			if profile.SameHost(entry.Profile) {
				entry.Profile = profile
				break
			}
		}
		if m.connectionConnected(entry.Profile) {
			m.openRecentRemoteProject(entry.Profile, entry.Path)
		} else {
			c.open = true
			m.beginConnection(entry.Profile, false)
			if c.busy {
				c.recentProject = entry.Path
			}
		}
		return
	}
	if c != nil && c.workspaceProfile.Remote() {
		c.open = true
		m.beginConnection(connection.Profile{}, false)
		if c.busy {
			c.recentProject = entry.Path
			if link := c.links[c.workspaceProfile.Endpoint()]; link != nil {
				link.lostHandled = true
			}
			if local := m.session.localBackend; local != nil && local != m.backend {
				_ = local.Close()
				m.session.closeSettingsWriter(local)
			}
		}
		return
	}
	if err := m.loadProject(entry.Path); err != nil {
		m.status = err.Error()
	}
}

func (m *workspaceModel) openRecentRemoteProject(profile connection.Profile, directory string) {
	c := m.session.connection
	if m.backend == c.links[profile.Endpoint()].backend {
		if err := m.loadProject(directory); err != nil {
			m.status = err.Error()
		}
		return
	}
	profile.Directory = directory
	m.openRemoteProject(profile)
	c.picker.autoOpen = true
}
