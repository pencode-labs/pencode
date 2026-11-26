package workbench

import (
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Workspace resources use canonical relative paths, so this key cannot alias a disk file.
const untitledPrefix = "./"

func (f *openFile) untitled() bool { return strings.HasPrefix(f.Path, untitledPrefix) }

func (m *workspaceModel) newUntitled() {
	p := m.activeProjectState()
	if p == nil {
		root := m.lastDirectory
		if root == "" || m.remoteWorkspace() {
			root = "~"
		}
		if err := m.submitFileIO(&fileIOJob{kind: fileUntitledWorkspace, root: root}); err != nil {
			m.setActiveStatus(err.Error())
		}
		return
	}
	m.untitledSequence++
	name := "Untitled-" + strconv.FormatUint(m.untitledSequence, 10)
	key := untitledPrefix + name
	if p.Files == nil {
		p.Files = make(map[string]*openFile)
	}
	file := &openFile{Path: key, Name: name, editorID: pencodeTextEditor}
	p.Files[key] = file
	p.Tabs = appendFileTab(p.Tabs, file)
	m.selectProjectTab(p, key)
	m.findOpen, m.replaceOpen, m.findNeedsFocus = false, false, false
	m.editor.needsFocus = true
}

func (m *workspaceModel) namingUntitled() bool {
	r := &m.closing
	return r.action != pencodeCloseNone && r.index < len(r.targets) && !r.lifecycle && r.targets[r.index].file.untitled()
}

func (m *workspaceModel) saveUntitled(target pencodeCloseTarget, name string) (*fileIOJob, error) {
	name = filepath.ToSlash(strings.TrimSpace(name))
	if !fs.ValidPath(name) || name == "." || strings.ContainsAny(name, "\\\x00") {
		return nil, fmt.Errorf("%s", m.tr("请输入有效的项目相对路径"))
	}
	p := target.project
	if p.Files[name] != nil {
		return nil, fmt.Errorf(m.tr("名称“%s”已存在"), name)
	}
	j := &fileIOJob{kind: fileSaveUntitled, project: p, file: target.file, relative: target.file.Path, destination: name, text: target.file.snapshot()}
	if err := m.submitResourceIO(j); err != nil {
		return nil, err
	}
	return j, nil
}

// Keep the editor buffer and its undo history when the first save assigns a disk path.
func (m *workspaceModel) nameUntitled(j *fileIOJob) {
	p, f := j.project, j.file
	m.highlightCache.remove(f)
	delete(p.Files, j.relative)
	f.Path, f.Name = j.destination, path.Base(j.destination)
	p.Files[f.Path] = f
	p.Tabs[slices.Index(p.Tabs, j.relative)] = f.Path
	if p.ActivePath == j.relative {
		p.ActivePath = f.Path
	}
	if m.closing.project == p && m.closing.path == j.relative {
		m.closing.path = f.Path
	}
	for i, tab := range m.closing.editorTabs {
		if tab.project == p && tab.path == j.relative {
			m.closing.editorTabs[i].path = f.Path
		}
	}
	m.syncActiveProjectFields()
	m.startProjectSnapshot(p, false, true)
}
