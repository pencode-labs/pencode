package workbench

import (
	"fmt"

	"github.com/onix-engine/onix/ecs"
	"github.com/onix-engine/onix/module"
	"github.com/onix-engine/onix/scene"
	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/protocol"
)

const pencodeTreeRowHeight = 30
const pencodeTreeOverscan = 8

// Expanded entry indexes are retained; -1 reserves the inline creation row.
// HTML components cover only the viewport.
type pencodeTreeView struct {
	source            *treeEntry
	directory         string
	rows              []int32
	revision, built   uint64
	indexed           int
	entries, expanded int
	first, end        int
	area              ecs.Entity
	position          float32
	restore           bool
	editing           bool
}

func (tree *pencodeTreeView) entriesChanged() {
	tree.source = nil
	tree.revision++
}

// Directory boundaries depend only on entries, so folding reuses this index.
// The fixed stack also handles a progressively published, unfinished subtree.
func (tree *pencodeTreeView) index(entries []treeEntry) {
	first := sourceFirst(entries)
	if tree.source == first && tree.indexed == len(entries) {
		return
	}
	var stack [protocol.IndexDepthLimit + 1]int32
	count := 0
	for i := range entries {
		for count > 0 && entries[stack[count-1]].Depth >= entries[i].Depth {
			count--
			entries[stack[count]].end = uint32(i)
		}
		if entries[i].IsDir {
			stack[count] = int32(i)
			count++
		}
	}
	for _, i := range stack[:count] {
		entries[i].end = uint32(len(entries))
	}
	tree.source, tree.indexed = first, len(entries)
}

func (project *pencodeProject) visibleTreeRows(model *workspaceModel) []int32 {
	tree := &project.tree
	c := &model.fileContext
	editing := c.project == project && c.inline()
	if tree.rows != nil && tree.source == sourceFirst(project.Entries) && tree.built == tree.revision && tree.entries == len(project.Entries) && tree.expanded == len(project.ExpandedDirs) && tree.editing == editing && (!editing || tree.directory == c.path) {
		return tree.rows
	}
	tree.index(project.Entries)
	tree.rows = tree.rows[:0]
	if editing && c.path == "" {
		tree.rows = append(tree.rows, -1)
	}
	for i := 0; i < len(project.Entries); {
		entry := project.Entries[i]
		tree.rows = append(tree.rows, int32(i))
		if editing && entry.IsDir && entry.Path == c.path {
			tree.rows = append(tree.rows, -1)
		}
		if entry.IsDir && !project.ExpandedDirs[entry.Path] {
			i = int(entry.end)
		} else {
			i++
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
	if project.tree.restore && project.scan == nil {
		position = min(0, max(project.tree.position, -max(0, float32(len(rows)*pencodeTreeRowHeight+18)-height)))
	}
	c := &model.fileContext
	editing := c.project == project && c.inline()
	if (model.revealProject == project || editing && c.needsFocus) && (project.scan == nil || project.scan.staged) {
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
			treeProps{Model: model, Theme: props.Theme, Icons: props.Icons, Project: project, TreeID: props.TreeID, Path: entry.Path, Name: entry.Name(), Depth: int(entry.Depth), IsDir: entry.IsDir, Openable: entry.Openable, HasErrors: !entry.IsDir && model.fileHasErrors(project, entry.Path)}, struct{}{})
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
		if scroll := world.Mut[scene.UIScroll](project.tree.area); scroll != nil {
			if project.tree.restore {
				// Wait for indexing and layout before the scroll system can clamp
				// the saved position against the complete content height.
				if project.scan == nil && scroll.ViewportSize.Y() > 0 && scroll.ContentSize.Y() >= float32(len(project.visibleTreeRows(model))*pencodeTreeRowHeight) {
					scroll.Position[1] = min(0, max(project.tree.position, -max(0, scroll.ContentSize.Y()-scroll.ViewportSize.Y())))
					scroll.Momentum[1] = 0
					project.tree.restore = false
				}
			} else {
				project.tree.position = scroll.Position.Y()
			}
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
