package workbench

import (
	"fmt"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	htmlui "github.com/diiyw/onix/ui/html"
)

const pencodeTreeRowHeight = 30
const pencodeTreeOverscan = 8

// Expanded entry indexes are retained; -1 reserves the inline creation row.
// HTML components cover only the viewport.
type pencodeTreeView struct {
	rows              []int
	revision, built   uint64
	entries, expanded int
	first, end        int
	area              ecs.Entity
	editing           bool
	directory         string
}

func (project *pencodeProject) visibleTreeRows(model *workspaceModel) []int {
	tree := &project.tree
	c := &model.fileContext
	editing := c.project == project && c.inline()
	if tree.rows != nil && tree.built == tree.revision && tree.entries == len(project.Entries) && tree.expanded == len(project.ExpandedDirs) && tree.editing == editing && (!editing || tree.directory == c.path) {
		return tree.rows
	}
	tree.rows = tree.rows[:0]
	if editing && c.path == "" {
		tree.rows = append(tree.rows, -1)
	}
	hiddenDepth := -1
	for i, entry := range project.Entries {
		if hiddenDepth >= 0 && entry.Depth > hiddenDepth {
			continue
		}
		hiddenDepth = -1
		tree.rows = append(tree.rows, i)
		if editing && entry.IsDir && entry.Path == c.path {
			tree.rows = append(tree.rows, -1)
		}
		if entry.IsDir && !project.ExpandedDirs[entry.Path] {
			hiddenDepth = entry.Depth
		}
	}
	tree.built, tree.entries, tree.expanded = tree.revision, len(project.Entries), len(project.ExpandedDirs)
	tree.editing, tree.directory = editing, c.path
	return tree.rows
}

func (project *pencodeProject) treeRange(world *module.Content, model *workspaceModel) (int, int) {
	rows := project.visibleTreeRows(model)
	height, position := float32(900), float32(0)
	if scroll := world.Ref[scene.UIScroll](project.tree.area); scroll != nil {
		height, position = scroll.ViewportSize.Y(), scroll.Position.Y()
	}
	c := &model.fileContext
	editing := c.project == project && c.inline()
	if (model.revealProject == project || editing && c.needsFocus) && project.scan == nil {
		for i, index := range rows {
			if editing && index != -1 || !editing && (index < 0 || project.Entries[index].Path != model.revealPath) {
				continue
			}
			position = min(0, max(height/2-float32(5+i*pencodeTreeRowHeight+pencodeTreeRowHeight/2), -max(0, float32(len(rows)*pencodeTreeRowHeight+18)-height)))
			if scroll := world.Mut[scene.UIScroll](project.tree.area); scroll != nil {
				scroll.Position[1] = position
			}
			break
		}
	}
	first := max(0, int(-position)/pencodeTreeRowHeight-pencodeTreeOverscan)
	end := min(len(rows), int((-position+height)/pencodeTreeRowHeight)+pencodeTreeOverscan+1)
	return min(first, end), end
}

func renderPencodeTree(context module.ContentRenderContext[projectProps, struct{}], view *htmlui.View, definition, creationDefinition *htmlui.Definition[treeProps, struct{}]) {
	props := context.Props
	project, model := props.Project, props.Model
	rows := project.visibleTreeRows(model)
	first, end := project.treeRange(context.World, model)
	project.tree.first, project.tree.end = first, end
	view.Attr("tree-height", "style", fmt.Sprintf("height:%dpx", len(rows)*pencodeTreeRowHeight))
	view.Attr("tree-window", "style", fmt.Sprintf("top:%dpx", first*pencodeTreeRowHeight))
	view.Components("tree-window", nil)
	for _, index := range rows[first:end] {
		if index < 0 {
			view.AppendComponentChild("tree-window", componentKey("\x00create"), creationDefinition,
				treeProps{Model: model, Theme: props.Theme, Icons: props.Icons, Project: project, TreeID: props.TreeID, Depth: model.fileContext.depth, Editing: true}, struct{}{})
			continue
		}
		entry := project.Entries[index]
		view.AppendComponentChild("tree-window", componentKey(entry.Path), definition,
			treeProps{Model: model, Theme: props.Theme, Icons: props.Icons, Project: project, TreeID: props.TreeID, Path: entry.Path, Name: entry.Name, Depth: entry.Depth, IsDir: entry.IsDir, Openable: entry.Openable, HasErrors: !entry.IsDir && model.fileHasErrors(project, entry.Path)}, struct{}{})
	}
}

// Runs after scrolling and before component reconciliation, including scrollbar drags.
func (model *workspaceModel) syncTreeScroll(world *module.Content) {
	if world == nil || model.sidebarHidden {
		return
	}
	for entity, access := range world.Query[htmlui.Component[projectProps, struct{}]]() {
		props := access.Get().Props
		if props.Model != model || props.Project == nil || props.Project.Collapsed {
			continue
		}
		project := props.Project
		if node, ok := world.HTMLNode(model.surface, props.TreeID); ok {
			project.tree.area, _ = node.ScrollContainer()
		}
		first, end := project.treeRange(world, model)
		if first != project.tree.first || end != project.tree.end {
			world.UpdateState[projectProps, struct{}](entity, func(*struct{}) {})
			if model.app != nil {
				model.app.RequestRender()
			}
		}
	}
}
