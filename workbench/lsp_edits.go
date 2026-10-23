package workbench

import (
	"encoding/json"
	"fmt"
	"github.com/pencode-labs/pencode/client"
	"path/filepath"
	"strings"

	"github.com/diiyw/onix/module"
	"github.com/pencode-labs/pencode/lsp"
)

// Plans own immutable snapshots until the foreground validates every target.
// No buffer is changed if any file, version, or language session became stale.
type languageEditTarget struct {
	uri                       lsp.URI
	file                      *openFile
	relative, before, content string
	edits                     []lsp.TextEdit
	version                   *int
	document                  *client.Document
}
type languageEditPlan struct {
	state    *pencodeLanguageState
	session  *pencodeLanguageSession
	client   *lsp.Client
	encoding lsp.PositionEncoding
	targets  []languageEditTarget
	complete func(error)
}

func (p *languageEditPlan) finish(err error) {
	if p.complete != nil {
		complete := p.complete
		p.complete = nil
		complete(err)
	}
}
func (p *languageEditPlan) release() {
	for i := range p.targets {
		if d := p.targets[i].document; d != nil {
			d.Release()
			p.targets[i].document = nil
		}
	}
}
func (model *workspaceModel) applyLanguageWorkspaceEdit(session *pencodeLanguageSession, edit lsp.WorkspaceEdit, complete func(error)) error {
	if model.languages == nil || session.client == nil {
		return fmt.Errorf("语言服务已停止")
	}
	var project *pencodeProject
	files := model.files
	for _, candidate := range model.projects {
		if candidate.Path == session.key.root {
			project, files = candidate, candidate.Files
			break
		}
	}
	if project == nil && model.projectPath != session.key.root {
		return fmt.Errorf("工作区已关闭")
	}
	if model.fileIOMutating(project) {
		return fmt.Errorf("请等待文件操作完成")
	}
	plan := &languageEditPlan{state: model.languages, session: session, client: session.client, encoding: session.client.Encoding, complete: complete}
	seen := make(map[lsp.URI]struct{}, len(edit.Changes)+len(edit.DocumentChanges))
	add := func(uri lsp.URI, edits []lsp.TextEdit, version *int) error {
		filename, err := uri.Path()
		if err != nil {
			return err
		}
		uri = lsp.FileURI(filename)
		if _, exists := seen[uri]; exists {
			return fmt.Errorf("同一文档出现在多个编辑中：%s", uri)
		}
		seen[uri] = struct{}{}
		relative, err := filepath.Rel(session.key.root, filename)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			return fmt.Errorf("编辑超出语言服务的工作区：%s", uri)
		}
		relative = filepath.ToSlash(relative)
		if !isTextFile(relative) {
			return fmt.Errorf("不能修改二进制文件：%s", relative)
		}
		t := languageEditTarget{uri: uri, relative: relative, file: files[relative], edits: edits, version: version}
		if t.file == nil {
			if version != nil {
				return fmt.Errorf("未打开文档不能使用指定版本：%s", relative)
			}
		} else {
			t.before = t.file.Content
			if version != nil {
				doc := model.languages.documents[t.file]
				if doc == nil || doc.version != *version || doc.content != t.before {
					return fmt.Errorf("文档版本已改变：%s", relative)
				}
			}
		}
		plan.targets = append(plan.targets, t)
		return nil
	}
	if edit.DocumentChanges == nil {
		for uri, edits := range edit.Changes {
			if err := add(uri, edits, nil); err != nil {
				return err
			}
		}
	}
	for _, raw := range edit.DocumentChanges {
		var change struct {
			Kind string `json:"kind"`
			lsp.TextDocumentEdit
		}
		if err := json.Unmarshal(raw, &change); err != nil {
			return err
		}
		if change.Kind != "" {
			return fmt.Errorf("不支持工作区资源操作：%s", change.Kind)
		}
		if err := add(change.TextDocument.URI, change.Edits, change.TextDocument.Version); err != nil {
			return err
		}
	}
	job := &fileIOJob{kind: fileLanguageEdit, project: project, root: session.key.root, language: plan}
	if project != nil {
		job.workspace = project.workspace.ID
	}
	return model.submitFileIO(job)
}
func executeLanguageEdit(j *fileIOJob) error {
	workspace := j.workspace
	for i := range j.language.targets {
		t := &j.language.targets[i]
		if err := j.ctx.Err(); err != nil {
			return err
		}
		if t.file == nil {
			if workspace == 0 {
				w, err := j.backend.OpenWorkspace(j.ctx, j.root)
				if err != nil {
					return err
				}
				workspace = w.ID
			}
			var err error
			t.document, t.before, err = j.backend.OpenDocument(j.ctx, workspace, t.relative, true)
			if err != nil {
				return err
			}
		}
		var err error
		t.content, err = lsp.ApplyTextEdits(t.before, t.edits, j.language.encoding)
		if err != nil {
			return err
		}
	}
	return nil
}
func (m *workspaceModel) acceptLanguageEdit(r *fileIOResult) {
	j, plan := r.job, r.job.language
	err := r.err
	if err == nil {
		if j.ctx.Err() != nil {
			err = j.ctx.Err()
		} else if m.languages != plan.state || plan.session.client != plan.client || plan.session.failed {
			err = fmt.Errorf("语言服务已改变，编辑未应用")
		} else if j.project != nil && !m.projectLive(j.project) || j.project == nil && m.projectPath != j.root {
			err = fmt.Errorf("工作区已关闭，编辑未应用")
		}
	}
	files := m.files
	if j.project != nil {
		files = j.project.Files
	}
	if err == nil {
		for _, t := range plan.targets {
			f := files[t.relative]
			if f != t.file || f != nil && f.Content != t.before {
				err = fmt.Errorf("文档已改变，编辑未应用：%s", t.relative)
				break
			}
			if t.version != nil {
				doc := m.languages.documents[f]
				if doc == nil || doc.version != *t.version || doc.content != t.before {
					err = fmt.Errorf("文档版本已改变：%s", t.relative)
					break
				}
			}
		}
	}
	if err == nil {
		for i := range plan.targets {
			t := &plan.targets[i]
			f := t.file
			if f == nil {
				f = &openFile{document: t.document, Path: t.relative, Name: filepath.Base(t.relative), Content: t.before, savedContent: t.before}
				t.document = nil
				files[t.relative] = f
				if j.project != nil {
					j.project.Tabs = append(j.project.Tabs, t.relative)
				} else {
					m.tabs = append(m.tabs, t.relative)
				}
			}
			m.applyFileContent(f, j.project, t.content, true)
		}
		m.syncActiveProjectFields()
		m.syncLanguageDocuments()
	} else {
		m.setProjectStatus(j.project, err.Error())
	}
	j.err = err
	plan.finish(err)
}

func (model *workspaceModel) navigateLanguageLocation(world *module.Content, location lsp.Location, selection *lsp.Range) bool {
	filename, err := location.URI.Path()
	if err != nil {
		model.setActiveStatus(err.Error())
		return false
	}
	var project *pencodeProject
	root := model.projectPath
	for _, candidate := range model.projects {
		relative, err := filepath.Rel(candidate.Path, filename)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			project = candidate
			root = candidate.Path
			break
		}
	}
	if root == "" {
		return false
	}
	relative, err := filepath.Rel(root, filename)
	if err != nil {
		return false
	}
	relative = filepath.ToSlash(relative)
	r := location.Range
	if selection != nil {
		r = *selection
	}
	encoding := lsp.UTF16
	if model.languages != nil && model.languages.panel.session != nil && model.languages.panel.session.client != nil {
		encoding = model.languages.panel.session.client.Encoding
	}
	if project != nil {
		model.activateProject(project)
	}
	navigation := &fileNavigation{offset: func(content string) (int, error) { return lsp.OffsetAt(content, r.Start, encoding) }}
	if err := model.requestFileOpen(project, relative, false, true, navigation); err != nil {
		model.setActiveStatus(err.Error())
		return false
	}
	return true
}
