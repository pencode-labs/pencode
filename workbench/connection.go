package workbench

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/configuration"
	"github.com/pencode-labs/pencode/connection"
	"github.com/pencode-labs/pencode/remote"
)

type pencodeDial func(context.Context, connection.Profile, func(string)) (*client.Client, error)

func (model *workspaceModel) historyProject() string {
	if project := model.activeProjectState(); project != nil && project.historyKey != "" {
		return project.historyKey
	}
	return model.projectPath
}

type pencodeConnectionProject struct {
	project *pencodeProject // Identity only; the worker never dereferences it.
	files   []*openFile
}

type pencodeSSHLink struct {
	profile     connection.Profile
	backend     *client.Client
	watchCancel context.CancelFunc
	connectedAt time.Time
	rapidLosses int
	lostHandled bool
}

type pencodeAuthenticationRequest struct {
	prompt remote.AuthenticationPrompt
	answer chan string
	ctx    context.Context
}

type pencodeConnectionResult struct {
	configuration configuration.Snapshot
	restore       bool
	backend       *client.Client
	profile       connection.Profile
	projects      []pencodeConnectionProject
	restored      []connection.RestoredProject
	err           error
}

type pencodeConnection struct {
	picker              pencodeProjectPicker
	links               map[remote.Target]*pencodeSSHLink
	workspaceProfile    connection.Profile
	restoring           bool
	passwordFocus       bool
	password            string
	passwordRequest     *pencodeAuthenticationRequest
	passwords           chan *pencodeAuthenticationRequest
	dial                pencodeDial
	profile, form       connection.Profile
	profiles            []connection.Profile
	port                string
	open, formOpen      bool
	busy, launch        bool
	edit                int
	menu                int
	menuX, menuY        float32
	lostHandled         bool
	connectedAt         time.Time
	rapidLosses         int
	message             string
	messageError        bool
	namespace           string
	results             chan pencodeConnectionResult
	progress            chan string
	cancel, watchCancel context.CancelFunc
	done                chan struct{}
}

func (c *pencodeConnection) setMessage(message string, failed bool) {
	c.message, c.messageError = message, failed
}

// ConfigureConnection enables the shared local/SSH lifecycle. Tests may inject
// a dial function without spawning an editor or using an external host.
func (s *Session) ConfigureConnection(dial func(context.Context, connection.Profile, func(string)) (*client.Client, error), initial connection.Profile) {
	profiles := connection.LoadProfiles(connection.ProfilePath())
	form := connection.Profile{}
	if initial.Remote() {
		form = initial
	}
	s.connection = &pencodeConnection{dial: dial, form: form, profiles: profiles, edit: -1, menu: -1, launch: initial.Remote(),
		results: make(chan pencodeConnectionResult, 1), progress: make(chan string, 1),
		links: make(map[remote.Target]*pencodeSSHLink), passwords: make(chan *pencodeAuthenticationRequest, 1)}
	if form.SSH.Port != 0 {
		s.connection.port = strconv.Itoa(int(form.SSH.Port))
	}
}

func (c *pencodeConnection) stop() {
	c.picker.stop()
	for _, link := range c.links {
		if link.watchCancel != nil {
			link.watchCancel()
		}
		if link.backend != nil {
			_ = link.backend.Close()
		}
	}
	c.password, c.passwordRequest = "", nil
	if c.watchCancel != nil {
		c.watchCancel()
		c.watchCancel = nil
	}
	if c.cancel != nil {
		c.cancel()
		<-c.done
		c.cancel = nil
	}
	select {
	case result := <-c.results:
		if result.backend != nil {
			_ = result.backend.Close()
		}
	default:
	}
}

func (s *Session) watchConnection(wake func()) {
	c := s.connection
	if c == nil || s.backend == nil || c.watchCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.watchCancel = cancel
	backend := s.backend
	go func() {
		select {
		case <-ctx.Done():
		case <-backend.Done():
			if wake != nil {
				wake()
			}
		}
	}()
}

// Retire all readers and handles before replacing a connection. The text,
// piece trees, undo stacks, source drafts and terminal screens remain local.
func (model *workspaceModel) retireConnection(closeBackend bool) {
	model.closeDebugger()
	model.closePorts()
	model.stopFileIO()
	model.fileClose = pencodeCloseRequest{}
	if model.closing.saveID != 0 {
		model.closing.saveID = 0
		model.closing.error = "连接已断开，请在重连后重试保存"
	}
	s := model.session
	if closeBackend && s.backend != nil {
		_ = s.backend.Close()
		s.closeSettingsWriter(s.backend)
	}
	s.market.stop()
	s.extensions.Close()
	s.extensions = nil
	model.closeActions()
	model.closeLanguageServers()
	model.closeCustomEditors()
	for view := range s.views {
		view.model.closeHighlightWorker()
		view.model.closeQuickWorker()
		view.model.closeFolderSearch(nil)
		view.model.closeFolderSearchWorker()
		view.model.closeHoverWorker()
		view.model.closeReferenceWorker()
	}
	if model.terminals != nil {
		for _, item := range model.terminals.items {
			item.session.Close()
			if !item.exited {
				item.label += "（连接已断开）"
			}
			item.exited = true
		}
	}
	for _, project := range model.projects {
		project.stopScan()
		project.sourceControl.stop()
		project.index = 0
		for _, file := range project.Files {
			file.closeDocument()
		}
	}
	model.sourceControl.stop()
}

func (model *workspaceModel) beginConnection(profile connection.Profile, retry bool) {
	s := model.session
	if s == nil || s.connection == nil || s.connection.busy {
		return
	}
	c := s.connection
	if profile.Remote() {
		if err := profile.SSH.Validate(); err != nil {
			c.setMessage(err.Error(), true)
			return
		}
	}
	if c.dial == nil {
		return
	}
	restore := !profile.Remote()
	if profile.Remote() {
		link := c.links[profile.SSH]
		if link == nil {
			link = &pencodeSSHLink{profile: profile}
			c.links[profile.SSH] = link
		}
		restore = s.backend == link.backend && link.backend != nil
		if !retry {
			link.rapidLosses = 0
		}
		if link.watchCancel != nil {
			link.watchCancel()
			link.watchCancel = nil
		}
		if link.backend != nil && !restore {
			_ = link.backend.Close()
			s.closeSettingsWriter(link.backend)
		}
		link.lostHandled = true
	}
	if !retry && restore {
		c.rapidLosses = 0
	}
	var projects []connection.Project
	var identities []pencodeConnectionProject
	if restore {
		projects = make([]connection.Project, len(model.projects))
		identities = make([]pencodeConnectionProject, len(model.projects))
		for i, project := range model.projects {
			projects[i].Path = project.Path
			identities[i].project = project
			for _, file := range project.Files {
				projects[i].Files = append(projects[i].Files, connection.File{Path: file.Path, Text: isTextFile(file.Name)})
				identities[i].files = append(identities[i].files, file)
			}
		}
		if c.watchCancel != nil {
			c.watchCancel()
			c.watchCancel = nil
		}
		model.retireConnection(true)
		c.lostHandled = true
	}
	c.profile = profile
	c.busy, c.restoring = true, restore
	c.setMessage("正在连接…", false)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	c.cancel, c.done = cancel, make(chan struct{})
	done, wake, dial := c.done, model.backgroundWake(), c.dial
	go func() {
		defer close(done)
		defer cancel()
		report := func(message string) {
			select {
			case <-c.progress:
			default:
			}
			select {
			case c.progress <- message:
			default:
			}
			if wake != nil {
				wake()
			}
		}
		prompt := func(ctx context.Context, message remote.AuthenticationPrompt) (string, error) {
			return c.requestAuthentication(ctx, message, wake)
		}
		ctx = remote.WithPrompt(ctx, prompt)
		result := pencodeConnectionResult{profile: profile, projects: identities, restore: restore}
		attempts := 1
		if retry {
			attempts = 3
		}
		for attempt := range attempts {
			if attempt > 0 {
				report(fmt.Sprintf("连接中断，正在进行第 %d 次重连…", attempt+1))
				timer := time.NewTimer(time.Duration(attempt) * time.Second)
				select {
				case <-ctx.Done():
					timer.Stop()
				case <-timer.C:
				}
			}
			if ctx.Err() != nil {
				result.err = ctx.Err()
				break
			}
			result.backend, result.err = dial(ctx, profile, report)
			if result.err == nil {
				break
			}
		}
		if result.err == nil && restore {
			result.configuration, result.err = result.backend.ReadConfiguration(ctx)
		}
		if result.err == nil && restore {
			report("正在恢复工作区和文档…")
			result.restored = connection.Restore(ctx, result.backend, projects)
			if err := ctx.Err(); err != nil {
				result.err = err
			}
		}
		if result.err != nil && result.backend != nil {
			_ = result.backend.Close()
			result.backend = nil
		}
		// One attempt owns the only result slot, including cancellation. The
		// foreground must always observe completion and leave the busy state.
		c.results <- result
		if wake != nil {
			wake()
		}
	}()
}

func (model *workspaceModel) pollConnection() bool {
	s := model.session
	if s == nil || s.connection == nil {
		return false
	}
	c := s.connection
	if c.launch {
		c.launch = false
		model.beginConnection(c.form, false)
		return true
	}
	changed := model.pollProjectPicker()
	select {
	case request := <-c.passwords:
		if request.ctx.Err() == nil {
			c.picker.stop()
			c.picker.open = false
			c.passwordRequest, c.password, c.passwordFocus = request, "", true
			c.open, c.formOpen, c.menu = true, false, -1
		}
		changed = true
	default:
	}
	if c.passwordRequest != nil && c.passwordRequest.ctx.Err() != nil {
		model.clearConnectionPassword()
		changed = true
	}
	select {
	case message := <-c.progress:
		c.setMessage(message, false)
		changed = true
	default:
	}
	select {
	case result := <-c.results:
		<-c.done
		select {
		case <-c.progress:
		default:
		}
		c.cancel, c.done, c.busy, c.restoring = nil, nil, false, false
		model.clearConnectionPassword()
		if result.err != nil {
			c.setMessage("连接失败："+result.err.Error(), true)
			c.open = true
			model.logOutput("SSH", c.message)
			return true
		}
		if result.profile.Remote() {
			link := c.links[result.profile.SSH]
			link.backend, link.profile = result.backend, result.profile
			link.connectedAt, link.lostHandled = time.Now(), false
			s.watchSSHLink(link, model.backgroundWake())
		}
		if result.restore {
			model.acceptConnection(result)
		} else {
			c.setMessage("SSH 已连接", false)
		}
		return true
	default:
	}
	if !c.busy {
		for _, link := range c.links {
			if link.backend == nil || link.backend == s.backend || link.lostHandled {
				continue
			}
			select {
			case <-link.backend.Done():
				if time.Since(link.connectedAt) >= 30*time.Second {
					link.rapidLosses = 0
				}
				link.rapidLosses++
				if link.rapidLosses > 3 {
					link.lostHandled, c.open = true, true
					c.setMessage("连接连续中断，已停止自动重连；请检查 SSH 后手动重连", true)
				} else {
					model.beginConnection(link.profile, true)
				}
				return true
			default:
			}
		}
	}
	if !c.busy && !c.lostHandled && s.backend != nil {
		select {
		case <-s.backend.Done():
			model.logOutput("SSH", fmt.Sprintf("连接已断开：%v", s.backend.Err()))
			if time.Since(c.connectedAt) >= 30*time.Second {
				c.rapidLosses = 0
			}
			c.rapidLosses++
			if c.rapidLosses > 3 {
				c.lostHandled, c.open = true, true
				c.setMessage("连接连续中断，已停止自动重连；请检查 SSH 后手动重连", true)
				model.retireConnection(true)
				return true
			}
			model.beginConnection(c.workspaceProfile, true)
			return true
		default:
		}
	}
	return changed
}

func (model *workspaceModel) acceptConnection(result pencodeConnectionResult) {
	s, backend := model.session, result.backend
	c := s.connection
	// Removing old registrations must not persist their temporary fallbacks.
	model.settingsPath = ""
	model.unloadContributions("extensions/", "peb://")
	clear(model.extensionStates)
	model.extensionFeedback = ""
	s.backend, model.backend = backend, backend
	model.applyConfiguration(result.configuration, result.profile.Remote())
	for view := range s.views {
		view.model.backend = backend
	}
	conflicts := 0
	for i, identity := range result.projects {
		project := identity.project
		index := model.projectIndex(project.Path)
		if index < 0 || model.projects[index] != project {
			for _, file := range result.restored[i].Files {
				if file.Document != nil {
					file.Document.Release()
				}
			}
			if workspace := result.restored[i].Workspace; workspace.ID != 0 {
				model.queueWorkspaceRelease(backend, workspace.ID)
			}
			continue
		}
		restored := result.restored[i]
		project.backend, project.workspace = backend, restored.Workspace
		project.sourceControl.Snapshot.Repository.Backend = backend
		project.sourceControl.Snapshot.Repository.Context = nil
		if restored.Err != nil {
			project.Status = "工作区恢复失败：" + restored.Err.Error()
			conflicts++
			continue
		}
		for j, file := range identity.files {
			state := restored.Files[j]
			if project.Files[file.Path] != file {
				if state.Document != nil {
					state.Document.Release()
				}
				continue
			}
			if state.Err != nil || state.Disk != file.savedContent && state.Disk != file.Content {
				if state.Document != nil {
					state.Document.Release()
				}
				file.Dirty = true
				conflicts++
				continue
			}
			file.document = state.Document
			if state.Disk == file.Content {
				file.savedContent, file.Dirty = state.Disk, false
			}
			if file.document != nil {
				file.document.Update("", file.Content, -1, 0, 0)
			}
		}
		model.startProjectScan(project, false)
	}
	c.workspaceProfile, c.namespace = result.profile, result.profile.Namespace()
	if !result.profile.Remote() {
		s.localBackend = backend
	}
	c.connectedAt = time.Now()
	c.lostHandled, c.open = false, false
	c.setMessage("已连接；未保存内容和撤销历史已保留", false)
	if conflicts > 0 {
		c.setMessage(fmt.Sprintf("已连接；%d 项恢复失败或远端内容已变化，本地内容已保留，请比较或重新加载后保存", conflicts), true)
		c.open = true
	}
	s.market.items, s.market.directory = nil, ""
	model.syncActiveProjectFields()
	model.resetHighlight()
	s.startExtensions(model)
	s.watchConnection(model.backgroundWake())
	if model.activeProjectState() != nil {
		model.refreshSourceControl()
	}
}

type pencodeConnectionProps struct {
	Model   *workspaceModel
	Profile connection.Profile
	Index   int
}

type pencodeConnectionState struct{}

func (model *workspaceModel) connectionConnected(profile connection.Profile) bool {
	s := model.session
	if s == nil || s.connection == nil {
		return false
	}
	link := s.connection.links[profile.SSH]
	return link != nil && link.backend != nil && link.backend.Err() == nil && !link.lostHandled
}

func renderPencodeConnection(c module.ContentRenderContext[pencodeConnectionProps, pencodeConnectionState], view *htmlui.View) {
	p := c.Props
	if p == nil || p.Model == nil || p.Model.session == nil || p.Model.session.connection == nil {
		return
	}
	manager := p.Model.session.connection
	connected := p.Model.connectionConnected(p.Profile)
	class := "pencode-connection-row"
	if connected {
		class += " connected"
	}
	view.Attr("row", "class", class)
	dotClass := "pencode-connection-dot"
	if connected {
		dotClass += " connected"
	}
	view.Attr("dot", "class", dotClass)
	view.Text("name", p.Profile.Label())
	view.Text("host", p.Profile.SSH.Host)
	view.Attr("more", "id", fmt.Sprintf("connection-more-%d", p.Index))
	view.Attr("more-icon", "src", p.Model.themedIcon("icons/ellipsis.svg", "icon.foreground"))
	view.Attr("project-icon", "src", p.Model.themedIcon("icons/folder-opened.svg", "icon.foreground"))
	view.Event("more", htmlui.EventClick)
	view.Event("project", htmlui.EventClick)
	if manager.menu == p.Index {
		view.Attr("more", "aria-expanded", "true")
	} else {
		view.Attr("more", "aria-expanded", "false")
	}
	if connected {
		view.RemoveAttr("project", "disabled")
	} else {
		view.Attr("project", "disabled", "")
	}
}

func handlePencodeConnection(c *module.ContentEventContext[pencodeConnectionProps, pencodeConnectionState]) {
	p := c.Props
	if p == nil || p.Model == nil || p.Model.session == nil || p.Model.session.connection == nil {
		return
	}
	manager := p.Model.session.connection
	switch c.Ref {
	case "more":
		if manager.menu == p.Index {
			manager.menu = -1
			break
		}
		manager.menu = p.Index
		// Anchor the overlay menu to the button so opening it never grows the list.
		root, rootOK := c.World.HTMLNode(p.Model.surface, "pencode-root")
		anchor, anchorOK := c.World.HTMLNode(p.Model.surface, fmt.Sprintf("connection-more-%d", p.Index))
		area, areaOK := root.Bounds()
		box, boxOK := anchor.Bounds()
		if rootOK && anchorOK && areaOK && boxOK && area.Width > 0 && area.Height > 0 {
			const menuWidth, menuHeight, gap, margin = float32(164), float32(134), float32(4), float32(8)
			x := min(max(box.X+box.Width-menuWidth-area.X, margin), max(margin, area.Width-menuWidth-margin))
			y := box.Y + box.Height + gap - area.Y
			if y+menuHeight > area.Height-margin {
				y = max(margin, box.Y-area.Y-menuHeight-gap)
			}
			manager.menuX, manager.menuY = x, y
		}
	case "project":
		manager.menu = -1
		if p.Model.connectionConnected(p.Profile) {
			p.Model.openSSHProject(p.Profile)
		}
	}
	markPencodeRootDirtyByModel(c.World, p.Model)
}

func (model *workspaceModel) retryConnection(profile connection.Profile) {
	model.session.connection.rapidLosses = 0
	if link := model.session.connection.links[profile.SSH]; link != nil {
		link.rapidLosses = 0
	}
	model.beginConnection(profile, true)
}

func (model *workspaceModel) saveConnectionForm() {
	c := model.session.connection
	profile := c.form
	profile.Name = strings.TrimSpace(profile.Name)
	profile.SSH.Host = strings.TrimSpace(profile.SSH.Host)
	if profile.Name == "" {
		c.setMessage("请输入显示名称", true)
		return
	}
	port, err := strconv.ParseUint(strings.TrimSpace(c.port), 10, 16)
	if strings.TrimSpace(c.port) == "" {
		port, err = 0, nil
	}
	if err != nil || c.port != "" && port == 0 {
		c.setMessage("SSH 端口必须在 1–65535 之间", true)
		return
	}
	profile.SSH.Port = uint16(port)
	if err := profile.SSH.Validate(); err != nil {
		c.setMessage(err.Error(), true)
		return
	}
	for index := range c.profiles {
		if index != c.edit && c.profiles[index].Name == profile.Name {
			c.setMessage("显示名称已存在", true)
			return
		}
	}
	profiles := make([]connection.Profile, len(c.profiles), len(c.profiles)+1)
	copy(profiles, c.profiles)
	var previous connection.Profile
	active := false
	if c.edit >= 0 && c.edit < len(profiles) {
		previous = profiles[c.edit]
		active = model.connectionConnected(previous)
		profiles[c.edit] = profile
	} else {
		profiles = append(profiles, profile)
	}
	if err := connection.SaveProfiles(connection.ProfilePath(), profiles); err != nil {
		c.setMessage("连接设置保存失败："+err.Error(), true)
		return
	}
	c.profiles, c.form, c.formOpen, c.edit = profiles, profile, false, -1
	c.setMessage("已保存 SSH 连接", false)
	if link := c.links[previous.SSH]; link != nil && previous.SSH == profile.SSH {
		link.profile = profile
		if c.workspaceProfile.SSH == profile.SSH {
			c.workspaceProfile = profile
		}
	} else if active {
		model.beginConnection(profile, false)
	}
}

func (model *workspaceModel) removeConnection(index int, profile connection.Profile) {
	c := model.session.connection
	if index < 0 || index >= len(c.profiles) || c.profiles[index] != profile {
		return
	}
	if model.connectionConnected(profile) {
		c.setMessage("请先退出连接，再删除此 SSH 连接", true)
		return
	}
	profiles := make([]connection.Profile, len(c.profiles)-1)
	copy(profiles, c.profiles[:index])
	copy(profiles[index:], c.profiles[index+1:])
	if err := connection.SaveProfiles(connection.ProfilePath(), profiles); err != nil {
		c.setMessage("连接设置保存失败："+err.Error(), true)
		return
	}
	c.profiles = profiles
	c.setMessage("已删除 SSH 连接", false)
}

func (model *workspaceModel) connectionEvent(ref, value string) bool {
	if !strings.HasPrefix(ref, "connection-") {
		return false
	}
	if model.session == nil || model.session.connection == nil {
		return true
	}
	c := model.session.connection
	if strings.HasPrefix(ref, "connection-project-") {
		model.projectPickerEvent(ref, value)
		return true
	}
	switch ref {
	case "connection-menu-edit", "connection-menu-retry", "connection-menu-disconnect", "connection-menu-delete":
		if c.menu < 0 || c.menu >= len(c.profiles) {
			break
		}
		index, profile := c.menu, c.profiles[c.menu]
		c.menu = -1
		switch ref {
		case "connection-menu-edit":
			c.form, c.formOpen, c.edit = profile, true, index
			c.port = ""
			if profile.SSH.Port != 0 {
				c.port = strconv.Itoa(int(profile.SSH.Port))
			}
			c.setMessage("", false)
		case "connection-menu-retry":
			if model.connectionConnected(profile) {
				model.retryConnection(profile)
			} else {
				model.beginConnection(profile, false)
			}
		case "connection-menu-disconnect":
			if model.connectionConnected(profile) {
				model.disconnectSSH(profile)
			}
		case "connection-menu-delete":
			model.removeConnection(index, profile)
		}
	case "connection-open":
		c.open, c.menu = !c.open, -1
		if c.open {
			model.quickOpen, model.settingsOpen = false, false
			if model.market != nil {
				model.market.open = false
			}
		} else {
			c.formOpen = false
			c.picker.stop()
			c.picker.open = false
		}
	case "connection-close":
		c.picker.stop()
		c.picker.open = false
		c.open, c.formOpen, c.menu = false, false, -1
	case "connection-add":
		c.form, c.port = connection.Profile{}, ""
		c.setMessage("", false)
		c.formOpen, c.edit, c.menu = true, -1, -1
	case "connection-form-close", "connection-form-cancel":
		c.formOpen, c.edit, c.menu = false, -1, -1
		c.setMessage("", false)
	case "connection-name":
		c.form.Name = value
	case "connection-host":
		c.form.SSH.Host = value
	case "connection-port":
		c.port = value
	case "connection-identity":
		c.form.SSH.IdentityFile = value
	case "connection-directory":
		c.form.Directory = value
	case "connection-save":
		model.saveConnectionForm()
	case "connection-password":
		c.password = value
	case "connection-password-submit":
		if request := c.passwordRequest; request != nil && !strings.ContainsAny(c.password, "\r\n") {
			answer := c.password
			if request.prompt.Confirm {
				answer = "yes"
			}
			select {
			case request.answer <- answer:
			default:
			}
			model.clearConnectionPassword()
		}
	case "connection-password-cancel":
		if c.cancel != nil {
			c.cancel()
		}
		model.clearConnectionPassword()
	case "connection-cancel":
		if c.cancel != nil {
			c.cancel()
		}
	}
	return true
}

func (model *workspaceModel) renderConnection(view *htmlui.View, definition *htmlui.Definition[pencodeConnectionProps, pencodeConnectionState]) {
	view.Event("connection-open", htmlui.EventClick)
	if model.session == nil || model.session.connection == nil {
		return
	}
	c := model.session.connection
	panelClass := "pencode-connection"
	if c.formOpen || c.passwordRequest != nil {
		panelClass += " form-open"
	}
	view.Attr("connection-panel", "class", panelClass)
	view.Attr("connection-open", "title", "远程管理")
	view.Attr("connection-open-icon", "src", model.themedIcon("icons/remote.svg", "icon.foreground"))
	view.Attr("connection-close-icon", "src", model.themedIcon("icons/close.svg", "icon.foreground"))
	view.Attr("connection-form-close-icon", "src", model.themedIcon("icons/close.svg", "icon.foreground"))
	view.Text("connection-feedback", c.message)
	feedbackClass, feedbackRole, feedbackLive := "pencode-connection-feedback", "status", "polite"
	if c.messageError {
		feedbackClass, feedbackRole, feedbackLive = "pencode-connection-feedback error", "alert", "assertive"
	}
	view.Attr("connection-feedback", "class", feedbackClass)
	view.Attr("connection-feedback", "role", feedbackRole)
	view.Attr("connection-feedback", "aria-live", feedbackLive)
	for _, ref := range [...]string{"connection-menu-edit", "connection-menu-retry", "connection-menu-disconnect", "connection-menu-delete"} {
		view.Event(ref, htmlui.EventClick)
	}
	if c.menu >= 0 && c.menu < len(c.profiles) {
		view.RemoveAttr("connection-menu", "hidden")
		view.Attr("connection-menu", "style", fmt.Sprintf("left:%gpx;top:%gpx", c.menuX, c.menuY))
		if model.connectionConnected(c.profiles[c.menu]) {
			view.RemoveAttr("connection-menu-disconnect", "disabled")
			view.Attr("connection-menu-delete", "disabled", "")
		} else {
			view.Attr("connection-menu-disconnect", "disabled", "")
			view.RemoveAttr("connection-menu-delete", "disabled")
		}
		if c.busy {
			view.Attr("connection-menu-retry", "disabled", "")
		} else {
			view.RemoveAttr("connection-menu-retry", "disabled")
		}
	} else {
		view.Attr("connection-menu", "hidden", "")
	}
	if c.passwordRequest != nil {
		c.open = true
		view.RemoveAttr("connection-password-form", "hidden")
		view.Text("connection-password-prompt", c.passwordRequest.prompt.Text)
		if c.passwordRequest.prompt.Confirm {
			view.Text("connection-password-title", "确认 SSH 主机")
			view.Text("connection-password-note", "首次连接此主机。请核对上方主机指纹，确认后将记录到 SSH known_hosts。")
			view.Text("connection-password-submit", "信任并连接")
			view.Attr("connection-password", "hidden", "")
			view.Attr("connection-password-label", "hidden", "")
		} else {
			view.Text("connection-password-title", "SSH 身份验证")
			view.Text("connection-password-note", "密码仅用于本次认证，不会保存；断线重连时会重新询问。")
			view.Text("connection-password-submit", "连接")
			view.RemoveAttr("connection-password", "hidden")
			view.RemoveAttr("connection-password-label", "hidden")
		}
	} else {
		view.Attr("connection-password-form", "hidden", "")
	}
	view.Event("connection-password", htmlui.EventInput)
	view.Event("connection-password-submit", htmlui.EventClick)
	view.Event("connection-password-cancel", htmlui.EventClick)
	view.Attr("connection-password", "value", c.password)
	if c.open && !c.picker.open {
		view.RemoveAttr("connection-panel", "hidden")
	} else {
		view.Attr("connection-panel", "hidden", "")
		view.Attr("connection-open", "aria-expanded", "false")
		return
	}
	view.Attr("connection-open", "aria-expanded", boolAttribute(c.open))
	view.Attr("connection-panel", "aria-busy", boolAttribute(c.busy && c.passwordRequest == nil))
	for _, ref := range [...]string{"connection-close", "connection-add", "connection-form-close", "connection-form-cancel", "connection-save", "connection-cancel"} {
		view.Event(ref, htmlui.EventClick)
	}
	for _, field := range [...]struct{ ref, value string }{
		{"connection-name", c.form.Name}, {"connection-host", c.form.SSH.Host}, {"connection-port", c.port},
		{"connection-identity", c.form.SSH.IdentityFile}, {"connection-directory", c.form.Directory},
	} {
		view.Event(field.ref, htmlui.EventInput)
		view.Attr(field.ref, "value", field.value)
	}
	if c.formOpen {
		view.RemoveAttr("connection-form", "hidden")
		title := "添加 SSH 连接"
		if c.edit >= 0 {
			title = "编辑 SSH 连接"
		}
		view.Text("connection-form-title", title)
	} else {
		view.Attr("connection-form", "hidden", "")
	}
	if c.busy {
		view.Attr("connection-add", "disabled", "")
		view.Attr("connection-save", "disabled", "")
		view.RemoveAttr("connection-cancel", "hidden")
	} else {
		view.RemoveAttr("connection-add", "disabled")
		view.RemoveAttr("connection-save", "disabled")
		view.Attr("connection-cancel", "hidden", "")
	}
	for index := range c.profiles {
		profile := c.profiles[index]
		htmlui.AppendComponentChild(view, "connection-list", componentKey(profile.Name+"\x00"+profile.SSH.Host), definition,
			pencodeConnectionProps{Model: model, Profile: profile, Index: index}, pencodeConnectionState{})
	}
	if len(c.profiles) == 0 {
		view.Attr("connection-list", "hidden", "")
		view.RemoveAttr("connection-empty", "hidden")
	} else {
		view.RemoveAttr("connection-list", "hidden")
		view.Attr("connection-empty", "hidden", "")
	}
}

func (s *Session) watchSSHLink(link *pencodeSSHLink, wake func()) {
	ctx, cancel := context.WithCancel(context.Background())
	link.watchCancel = cancel
	backend := link.backend
	go func() {
		select {
		case <-ctx.Done():
		case <-backend.Done():
			if wake != nil {
				wake()
			}
		}
	}()
}

func (model *workspaceModel) clearConnectionPassword() {
	c := model.session.connection
	c.password, c.passwordRequest, c.passwordFocus = "", nil, false
	model.clearPasswordControl()
	for view := range model.session.views {
		if view.model != model {
			view.model.clearPasswordControl()
		}
	}
}

func (model *workspaceModel) disconnectSSH(profile connection.Profile) {
	c := model.session.connection
	link := c.links[profile.SSH]
	if link == nil {
		return
	}
	if link.watchCancel != nil {
		link.watchCancel()
		link.watchCancel = nil
	}
	link.lostHandled = true
	if link.backend == model.session.backend {
		if c.watchCancel != nil {
			c.watchCancel()
			c.watchCancel = nil
		}
		model.retireConnection(true)
		c.lostHandled = true
	} else if link.backend != nil {
		_ = link.backend.Close()
		model.session.closeSettingsWriter(link.backend)
	}
	c.setMessage("SSH 已断开", false)
}

// Opening a project starts with a directory choice on the connected device.
func (model *workspaceModel) openSSHProject(profile connection.Profile) {
	c := model.session.connection
	if !model.connectionConnected(profile) {
		return
	}
	c.picker.stop()
	c.picker = pencodeProjectPicker{profile: profile, open: true, connectionOpen: c.open}
	c.open, c.formOpen, c.menu = true, false, -1
	directory := profile.Directory
	if directory == "" {
		directory = "~"
	}
	c.picker.input = directory
	model.browseProjectDirectory(directory)
}

func (c *pencodeConnection) requestAuthentication(ctx context.Context, message remote.AuthenticationPrompt, wake func()) (string, error) {
	request := &pencodeAuthenticationRequest{prompt: message, answer: make(chan string, 1), ctx: ctx}
	select {
	case c.passwords <- request:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if wake != nil {
		wake()
	}
	select {
	case value := <-request.answer:
		return value, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (model *workspaceModel) focusConnectionPassword(world *module.Content) {
	if model.session == nil || model.session.connection == nil {
		return
	}
	c := model.session.connection
	if !c.passwordFocus || c.passwordRequest == nil || world == nil {
		return
	}
	if c.passwordRequest.prompt.Confirm {
		if field, ok := world.HTMLControl(model.surface, "connection-password-cancel"); ok && world.Focus(field) {
			c.passwordFocus = false
		}
		return
	}
	field, ok := world.HTMLControl(model.surface, "connection-password")
	if !ok {
		return
	}
	if !module.ContentHas[ui.TextField](world, field) {
		if err := world.SyncHTML(); err != nil {
			return
		}
		field, ok = world.HTMLControl(model.surface, "connection-password")
		if !ok || !module.ContentHas[ui.TextField](world, field) {
			return
		}
	}
	if world.Focus(field) {
		c.passwordFocus = false
	}
}

func (model *workspaceModel) clearPasswordControl() {
	if model.fontResources != nil {
		if field, ok := model.fontResources.HTMLControl(model.surface, "connection-password"); ok {
			model.fontResources.SetControlValue(field, "")
		}
	}
}
