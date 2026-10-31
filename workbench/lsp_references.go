package workbench

import (
	"context"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/pencode-labs/pencode/lsp"
)

type pencodeReferencePreview struct {
	group      int
	root, path string
	rows       []pencodeLanguageRow
}
type pencodeReferenceJob struct {
	groups          []pencodeReferencePreview
	encoding        lsp.PositionEncoding
	revision, token uint64
}
type pencodeReferenceResult struct{ job pencodeReferenceJob }

func (model *workspaceModel) pollReferencePreviews() bool {
	if model.referenceWorker == nil {
		return false
	}
	changed := false
	model.referenceWorker.poll(func(result *pencodeReferenceResult) {
		if model.languages == nil {
			return
		}
		panel := &model.languages.panel
		if result.job.token != panel.token || result.job.revision != model.referenceRevision {
			return
		}
		model.referenceApplied = result.job.revision
		for _, preview := range result.job.groups {
			if preview.group >= len(panel.referenceGroups) {
				continue
			}
			group := &panel.referenceGroups[preview.group]
			if len(group.rows) != len(preview.rows) || len(group.rows) == 0 || group.rows[0].location.URI != preview.rows[0].location.URI {
				continue
			}
			copy(group.rows, preview.rows)
		}
		selection, page := panel.selection, panel.page
		panel.rebuildReferenceRows(-1)
		panel.selection, panel.page = selection, page
		changed = true
	})
	return changed
}
func (model *workspaceModel) closeReferenceWorker() {
	if model.referenceWorker != nil {
		model.referenceCancel()
		model.referenceWorker.close()
		model.referenceWorker = nil
	}
}

type pencodeReferenceGroup struct {
	header   pencodeLanguageRow
	rows     []pencodeLanguageRow
	expanded bool
}

func (panel *pencodeLanguagePanel) appendReferenceLocation(location lsp.Location, root string, groups map[lsp.URI]int) {
	index, exists := groups[location.URI]
	if !exists {
		name := string(location.URI)
		if path, err := location.URI.Path(); err == nil {
			name = path
			if root != "" {
				if relative, err := filepath.Rel(root, path); err == nil {
					name = relative
				}
			}
		}
		index = len(panel.referenceGroups)
		groups[location.URI] = index
		fileName := filepath.Base(name)
		panel.referenceGroups = append(panel.referenceGroups, pencodeReferenceGroup{
			header: pencodeLanguageRow{
				label: fileName, fileName: fileName, path: filepath.ToSlash(filepath.Dir(name)),
				kind: "reference-file", group: index,
			},
		})
	}
	group := &panel.referenceGroups[index]
	group.rows = append(group.rows, pencodeLanguageRow{
		location: &location, kind: "reference",
	})
}

func (model *workspaceModel) prepareReferencePreviews(groups map[lsp.URI]int) {
	panel := &model.languages.panel
	model.referenceRevision++
	loaded := make([]bool, len(panel.referenceGroups))
	encoding := lsp.UTF16
	if panel.session != nil {
		encoding = panel.session.client.Encoding
	}
	var lineStarts []int
	visit := func(root string, files map[string]*openFile) {
		for _, file := range files {
			uri := lsp.FileURI(filepath.Join(root, filepath.FromSlash(file.Path)))
			if index, exists := groups[uri]; exists && !loaded[index] {
				lineStarts = panel.referenceGroups[index].setPreviews(file.content(), lineStarts, encoding)
				loaded[index] = true
			}
		}
	}
	if len(model.projects) == 0 {
		visit(model.projectPath, model.files)
	} else {
		for _, project := range model.projects {
			visit(project.Path, project.Files)
		}
	}

	var pending []pencodeReferencePreview
	for i := range panel.referenceGroups {
		if loaded[i] {
			continue
		}
		group := &panel.referenceGroups[i]
		filename, _ := group.rows[0].location.URI.Path()
		root := model.projectPath
		for _, project := range model.projects {
			if relative, err := filepath.Rel(project.Path, filename); err == nil && filepath.IsLocal(relative) {
				root = project.Path
				break
			}
		}
		relative, err := filepath.Rel(root, filename)
		if err != nil || !filepath.IsLocal(relative) {
			for j := range group.rows {
				group.rows[j].label = "无法读取源码"
			}
			continue
		}
		for j := range group.rows {
			group.rows[j].label = "正在加载源码…"
		}
		pending = append(pending, pencodeReferencePreview{group: i, root: root, path: filepath.ToSlash(relative), rows: slices.Clone(group.rows)})
	}
	if len(pending) == 0 {
		model.referenceApplied = model.referenceRevision
		return
	}
	if model.referenceWorker == nil {
		ctx, cancel := context.WithCancel(context.Background())
		model.referenceCancel = cancel
		backend := model.backend
		model.referenceWorker = newLatestWorker(func(job pencodeReferenceJob, result *pencodeReferenceResult) bool {
			result.job = job
			var starts []int
			for i := range job.groups {
				group := &job.groups[i]
				request, stop := context.WithTimeout(ctx, 10*time.Second)
				data, err := backend.ReadFile(request, group.root, group.path)
				stop()
				if err != nil {
					for j := range group.rows {
						group.rows[j].label = "无法读取源码"
					}
					continue
				}
				preview := pencodeReferenceGroup{rows: group.rows}
				starts = preview.setPreviews(string(data), starts, job.encoding)
			}
			return true
		}, func(result *pencodeReferenceResult) { *result = pencodeReferenceResult{} }, nil, model.backgroundWake())
	}
	model.referenceWorker.submit(pencodeReferenceJob{groups: pending, encoding: encoding, revision: model.referenceRevision, token: panel.token})

}

func (group *pencodeReferenceGroup) setPreviews(content string, lineStarts []int, encoding lsp.PositionEncoding) []int {
	lineStarts = append(lineStarts[:0], 0)
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			lineStarts = append(lineStarts, i+1)
		}
	}
	for i := range group.rows {
		row := &group.rows[i]
		row.matchStart, row.matchEnd = 0, 0
		line := row.location.Range.Start.Line
		if line < 0 || line >= len(lineStarts) {
			row.label = "源码位置已失效"
			continue
		}
		end := len(content)
		if line+1 < len(lineStarts) {
			end = lineStarts[line+1] - 1
		}
		source := content[lineStarts[line]:end]
		left := strings.TrimLeftFunc(source, unicode.IsSpace)
		row.label = strings.TrimRightFunc(left, unicode.IsSpace)
		reference := row.location.Range
		start, err := lsp.OffsetAt(source, lsp.Position{Character: reference.Start.Character}, encoding)
		if err != nil {
			continue
		}
		end = len(source)
		if reference.End.Line == line {
			length, err := lsp.OffsetAt(source[start:], lsp.Position{Character: reference.End.Character - reference.Start.Character}, encoding)
			if err != nil {
				continue
			}
			end = start + length
		} else if reference.End.Line < line {
			continue
		}
		trimmed := len(source) - len(left)
		row.matchStart = max(0, start-trimmed)
		row.matchEnd = min(len(row.label), end-trimmed)
	}
	return lineStarts
}

func (panel *pencodeLanguagePanel) rebuildReferenceRows(selectedGroup int) {
	clear(panel.rows)
	panel.rows = panel.rows[:0]
	panel.selection = 0
	for i := range panel.referenceGroups {
		group := &panel.referenceGroups[i]
		if group.header.count == "" {
			group.header.count = strconv.Itoa(len(group.rows))
		}
		if i == selectedGroup {
			panel.selection = len(panel.rows)
		}
		panel.rows = append(panel.rows, group.header)
		if group.expanded {
			panel.rows = append(panel.rows, group.rows...)
		}
	}
	panel.page = panel.selection / len(pencodeLanguageRowRefs)
}
