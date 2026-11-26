package workbench

import (
	"fmt"
	"path"
	"slices"

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
	selection         pencodeTreeSelection
	rows              []int32
	revision, built   uint64
	indexed           int
	entries, expanded int
	first, end        int
	area              ecs.Entity
	position          float32
	restore           bool
	creating          bool
	moved             bool // The explorer has applied moves ahead of the search index.
}

func (tree *pencodeTreeView) entriesChanged() {
	tree.source = nil
	tree.revision++
}

// Move a contiguous preorder subtree, keeping directories before files among
// its new siblings. Only the moved paths need new strings.
func (source *pencodeProject) moveTreeEntries(target *pencodeProject, old, next string) bool {
	find := func(entries []treeEntry, name string) int {
		return slices.IndexFunc(entries, func(entry treeEntry) bool { return entry.Path == name })
	}
	first := find(source.Entries, old)
	if first < 0 {
		return false
	}
	end := first + 1
	for end < len(source.Entries) && source.Entries[end].Depth > source.Entries[first].Depth {
		end++
	}
	insert, depth := 0, 0
	if parent := path.Dir(next); parent != "." {
		i := find(target.Entries, parent)
		if i < 0 || !target.Entries[i].IsDir {
			return false
		}
		insert, depth = i+1, int(target.Entries[i].Depth)+1
	}
	name := path.Base(next)
	for insert < len(target.Entries) {
		entry := target.Entries[insert]
		if int(entry.Depth) < depth {
			break
		}
		if int(entry.Depth) == depth {
			if entry.Path == next {
				return false
			}
			if compareTreeNames(name, source.Entries[first].IsDir, entry.Name(), entry.IsDir) < 0 {
				break
			}
		}
		insert++
	}
	offset := depth - int(source.Entries[first].Depth)
	moved := source.Entries[first:end]
	for _, entry := range moved {
		if int(entry.Depth)+offset > protocol.IndexDepthLimit {
			return false
		}
	}
	// The common single-file case needs no temporary slice allocation.
	var item [1]treeEntry
	moved = append(item[:0], moved...)
	for i := range moved {
		moved[i].Path = next + moved[i].Path[len(old):]
		moved[i].Depth = uint16(int(moved[i].Depth) + offset)
	}
	if source == target && insert >= end {
		insert -= len(moved)
	}
	source.Entries = slices.Delete(source.Entries, first, end)
	target.Entries = slices.Insert(target.Entries, insert, moved...)
	source.tree.entriesChanged()
	if target != source {
		target.tree.entriesChanged()
	}
	return true
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
	creating := c.project == project && c.creating()
	if tree.rows != nil && tree.source == sourceFirst(project.Entries) && tree.built == tree.revision && tree.entries == len(project.Entries) && tree.expanded == len(project.ExpandedDirs) && tree.creating == creating && (!creating || tree.directory == c.path) {
		return tree.rows
	}
	tree.index(project.Entries)
	tree.rows = tree.rows[:0]
	if creating && c.path == "" {
		tree.rows = append(tree.rows, -1)
	}
	for i := 0; i < len(project.Entries); {
		entry := project.Entries[i]
		tree.rows = append(tree.rows, int32(i))
		if creating && entry.IsDir && entry.Path == c.path {
			tree.rows = append(tree.rows, -1)
		}
		if entry.IsDir && !project.ExpandedDirs[entry.Path] {
			i = int(entry.end)
		} else {
			i++
		}
	}
	tree.built, tree.entries, tree.expanded = tree.revision, len(project.Entries), len(project.ExpandedDirs)
	tree.creating, tree.directory = creating, c.path
	return tree.rows
}

func (project *pencodeProject) treeRange(world *module.Content, model *workspaceModel) (int, int) {
	rows := project.visibleTreeRows(model)
	height, position := float32(900), float32(0)
	if scroll := world.Ref[scene.UIScroll](project.tree.area); scroll != nil {
		height, position = scroll.ViewportSize.Y(), scroll.Position.Y()
	}
	if project.tree.restore && project.scan == nil {
		position = project.tree.position
	}
	// Layout clamps the native scrollbar to the refreshed content height.
	// Mount the rows at that position even while it still reports the old extent.
	minPosition := -max(0, float32(len(rows)*pencodeTreeRowHeight+18)-height)
	position = min(0, max(position, minPosition))
	c := &model.fileContext
	editing := c.project == project && c.inline() && c.needsFocus
	if (model.revealProject == project || editing) && (project.scan == nil || project.scan.staged) {
		target := model.revealPath
		if editing {
			target = c.path
		}
		creating := editing && c.creating()
		for i, index := range rows {
			if creating && index >= 0 || !creating && (index < 0 || project.Entries[index].Path != target) {
				continue
			}
			top := float32(5 + i*pencodeTreeRowHeight)
			if !editing || creating || top+position < 5 || top+position+pencodeTreeRowHeight > height-13 {
				position = min(0, max(height/2-top-pencodeTreeRowHeight/2, minPosition))
			}
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

func renderPencodeTree(context module.ContentRenderContext[projectProps, struct{}], view *htmlui.View, definition, editDefinition *htmlui.Definition[treeProps, treeRowState]) {
	props := context.Props
	project, model := props.Project, props.Model
	rows := project.visibleTreeRows(model)
	first, end := project.treeRange(context.World, model)
	project.tree.first, project.tree.end = first, end
	view.Attr("tree-height", "style", fmt.Sprintf("height:%dpx", len(rows)*pencodeTreeRowHeight))
	view.Attr("tree-window", "style", fmt.Sprintf("top:%dpx", first*pencodeTreeRowHeight))
	view.Components("tree-window", nil)
	c := &model.fileContext
	renaming := c.project == project && c.dialog == "rename"
	for _, index := range rows[first:end] {
		if index < 0 {
			view.AppendComponentChild("tree-window", componentKey("\x00create"), editDefinition,
				treeProps{Model: model, Theme: props.Theme, Icons: props.Icons, Project: project, TreeID: props.TreeID, Depth: c.depth, Editing: true}, treeRowState{})
			continue
		}
		entry := project.Entries[index]
		if renaming && entry.Path == c.path {
			view.AppendComponentChild("tree-window", componentKey("\x00rename"), editDefinition,
				treeProps{Model: model, Theme: props.Theme, Icons: props.Icons, Project: project, TreeID: props.TreeID, Path: entry.Path, Name: entry.Name(), Depth: int(entry.Depth), IsDir: entry.IsDir, Editing: true}, treeRowState{})
			continue
		}
		view.AppendComponentChild("tree-window", componentKey(entry.Path), definition,
			treeProps{Model: model, Theme: props.Theme, Icons: props.Icons, Project: project, TreeID: props.TreeID, Path: entry.Path, Name: entry.Name(), Depth: int(entry.Depth), IsDir: entry.IsDir, Openable: entry.Openable, Decoration: model.fileNameDecoration(project, entry)}, treeRowState{})
	}
}

// Synchronize the mounted project tree after input or after layout restores
// its scrollbar. A change after layout also needs a native scroll update.
func (model *workspaceModel) syncTreeScroll(world *module.Content) bool {
	if world == nil || model.sidebarHidden() {
		return false
	}
	var dirty ecs.Entity
	moved := false
	for entity, access := range world.Query[htmlui.Component[projectProps, struct{}]]() {
		props := access.Get().Props
		if props.Model != model || props.Project == nil {
			continue
		}
		project := props.Project
		if node, ok := world.HTMLNode(model.surface, props.TreeID); ok {
			project.tree.area, _ = node.ScrollContainer()
		}
		scroll := world.Ref[scene.UIScroll](project.tree.area)
		var previous float32
		if scroll != nil {
			previous = scroll.Position.Y()
			if project.tree.restore {
				// Wait for indexing and layout before the scroll system can clamp
				// the saved position against the complete content height.
				if project.scan == nil && scroll.ViewportSize.Y() > 0 && scroll.ContentSize.Y() >= float32(len(project.visibleTreeRows(model))*pencodeTreeRowHeight) {
					scroll = world.Mut[scene.UIScroll](project.tree.area)
					scroll.Position[1] = min(0, max(project.tree.position, -max(0, scroll.ContentSize.Y()-scroll.ViewportSize.Y())))
					scroll.Momentum[1] = 0
					project.tree.restore = false
				}
			} else {
				project.tree.position = scroll.Position.Y()
			}
		}
		first, end := project.treeRange(world, model)
		if scroll != nil && scroll.Position.Y() != previous {
			moved = true
		}
		if first != project.tree.first || end != project.tree.end {
			dirty = entity
		}
		break
	}
	if dirty == 0 {
		return moved
	}
	// Reconcile the scroll clip before replacing keyed rows. Newly mounted
	// labels otherwise inherit the previous viewport's paint bounds.
	if err := world.SyncHTML(); err != nil {
		model.setActiveStatus(err.Error())
	}
	world.UpdateState[projectProps, struct{}](dirty, func(*struct{}) {})
	if model.app != nil {
		model.app.RequestRender()
	}
	return true
}
