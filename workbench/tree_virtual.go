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

// Only expanded entry indexes are retained; HTML components cover the viewport.
type pencodeTreeView struct {
	rows              []int
	revision, built   uint64
	entries, expanded int
	first, end        int
	area              ecs.Entity
}

func (project *pencodeProject) visibleTreeRows() []int {
	tree := &project.tree
	if tree.rows != nil && tree.built == tree.revision && tree.entries == len(project.Entries) && tree.expanded == len(project.ExpandedDirs) {
		return tree.rows
	}
	tree.rows = tree.rows[:0]
	hiddenDepth := -1
	for i, entry := range project.Entries {
		if hiddenDepth >= 0 && entry.Depth > hiddenDepth {
			continue
		}
		hiddenDepth = -1
		tree.rows = append(tree.rows, i)
		if entry.IsDir && !project.ExpandedDirs[entry.Path] {
			hiddenDepth = entry.Depth
		}
	}
	tree.built, tree.entries, tree.expanded = tree.revision, len(project.Entries), len(project.ExpandedDirs)
	return tree.rows
}

func (project *pencodeProject) treeRange(world *module.Content, model *workspaceModel) (int, int) {
	rows := project.visibleTreeRows()
	height, position := float32(900), float32(0)
	if scroll := module.ContentRef[scene.UIScroll](world, project.tree.area); scroll != nil {
		height, position = scroll.ViewportSize.Y(), scroll.Position.Y()
	}
	if model.revealProject == project && project.scan == nil {
		for i, index := range rows {
			if project.Entries[index].Path != model.revealPath {
				continue
			}
			position = min(0, max(height/2-float32(5+i*pencodeTreeRowHeight+pencodeTreeRowHeight/2), -max(0, float32(len(rows)*pencodeTreeRowHeight+18)-height)))
			if scroll := module.ContentMut[scene.UIScroll](world, project.tree.area); scroll != nil {
				scroll.Position[1] = position
			}
			break
		}
	}
	first := max(0, int(-position)/pencodeTreeRowHeight-pencodeTreeOverscan)
	end := min(len(rows), int((-position+height)/pencodeTreeRowHeight)+pencodeTreeOverscan+1)
	return min(first, end), end
}

func renderPencodeTree(context module.ContentRenderContext[projectProps, struct{}], view *htmlui.View, definition *htmlui.Definition[treeProps, struct{}]) {
	props := context.Props
	project, model := props.Project, props.Model
	rows := project.visibleTreeRows()
	first, end := project.treeRange(context.World, model)
	project.tree.first, project.tree.end = first, end
	view.Attr("tree-height", "style", fmt.Sprintf("height:%dpx", len(rows)*pencodeTreeRowHeight))
	view.Attr("tree-window", "style", fmt.Sprintf("top:%dpx", first*pencodeTreeRowHeight))
	view.Components("tree-window", nil)
	for _, index := range rows[first:end] {
		entry := project.Entries[index]
		htmlui.AppendComponentChild(view, "tree-window", componentKey(entry.Path), definition,
			treeProps{Model: model, Theme: props.Theme, Icons: props.Icons, Project: project, TreeID: props.TreeID, Path: entry.Path, Name: entry.Name, Depth: entry.Depth, IsDir: entry.IsDir, Openable: entry.Openable}, struct{}{})
	}
}

// Runs after scrolling and before component reconciliation, including scrollbar drags.
func (model *workspaceModel) syncTreeScroll(world *module.Content) {
	if world == nil || model.sidebarHidden {
		return
	}
	for entity, access := range module.ContentQuery[htmlui.Component[projectProps, struct{}]](world) {
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
			module.ContentUpdateState[projectProps, struct{}](world, entity, func(*struct{}) {})
			if model.app != nil {
				model.app.RequestRender()
			}
		}
	}
}
