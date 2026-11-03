package workbench

import "github.com/pencode-labs/pencode/peb"

func (m *workspaceModel) beginExtensionClose(action pencodeCloseAction, project *pencodeProject, path string) bool {
	if action < pencodeCloseProject || len(m.actions.sessions) == 0 {
		return false
	}
	m.actions.closeToken++
	r := &m.closing
	r.action, r.project, r.path = action, project, path
	r.lifecycle, r.lifecycleToken = true, m.actions.closeToken
	r.lifecyclePending = make(map[string]bool, len(m.actions.sessions))
	r.message, r.needsFocus = "正在检查扩展的后台任务…", true
	m.settingsOpen, m.quickOpen = false, false
	m.capturingShortcut, m.quickNeedsFocus, m.findNeedsFocus = false, false, false
	m.quickFocusEditor = false
	event := peb.LifecycleEvent{Kind: peb.WindowClosing}
	if project != nil {
		event.Kind, event.ProjectPath = peb.ProjectClosing, project.Path
	}
	for id, session := range m.actions.sessions {
		if session.SubmitLifecycle(event, r.lifecycleToken) {
			r.lifecyclePending[id] = true
		} else {
			r.error += "扩展 " + id + " 暂时无法检查后台任务。\n"
		}
	}
	if len(r.lifecyclePending) == 0 {
		m.completeExtensionClose()
	}
	return true
}

func (m *workspaceModel) resolveExtensionClose(id string, token uint64, reply peb.LifecycleReply) {
	r := &m.closing
	if !r.lifecycle || r.lifecycleToken != token || !r.lifecyclePending[id] {
		return
	}
	delete(r.lifecyclePending, id)
	if reply.Message != "" {
		r.error += reply.Message + "\n"
	}
	if len(r.lifecyclePending) == 0 {
		m.completeExtensionClose()
	}
}

func (m *workspaceModel) completeExtensionClose() {
	r := &m.closing
	if r.error != "" {
		r.message, r.error = r.error+"是否继续关闭？", ""
		r.needsFocus = true
		return
	}
	m.approveExtensionClose()
}

func (m *workspaceModel) approveExtensionClose() {
	r := &m.closing
	r.lifecycle = false
	// Extensions may finish edits while replying. Collect the current dirty set.
	clear(r.targets)
	r.targets, r.index = r.targets[:0], 0
	if r.project != nil {
		m.collectCloseTargets(r.project)
	} else if len(m.projects) != 0 {
		for _, project := range m.projects {
			m.collectCloseTargets(project)
		}
	} else {
		for _, path := range m.tabs {
			if file := m.files[path]; file != nil && file.Dirty {
				r.targets = append(r.targets, pencodeCloseTarget{file: file})
			}
		}
	}
	if len(r.targets) != 0 {
		m.showCloseTarget()
	} else {
		m.finishClose()
	}
}

func (m *workspaceModel) notifyProjectClosed(path string) {
	for _, session := range m.actions.sessions {
		session.SubmitLifecycle(peb.LifecycleEvent{Kind: peb.ProjectClosed, ProjectPath: path}, 0)
	}
}
