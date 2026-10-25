package workbench

import (
	"path"
	"time"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
)

type pencodeFileDrag struct {
	source, target        *pencodeProject
	path, directory       string
	x, y                  float32
	depth                 int
	active, suppressClick bool
	expandAt              time.Time
}

func (drag *pencodeFileDrag) isTarget(project *pencodeProject, directory string) bool {
	return drag.active && drag.target == project && drag.directory == directory
}

func (model *workspaceModel) updateFileDrag(world *module.Content, state *input.State) bool {
	drag := &model.fileDrag
	if state.PointerPressed() || state.KeyPressed(input.KeyEnter) || state.KeyPressed(input.KeySpace) {
		drag.suppressClick = false
	}
	if model.folderSearch.open || model.screen != pencodeWorkspace || model.sidebarHidden || model.settingsOpen || model.quickOpen || model.fileContext.open || model.fileContext.dialog != "" || model.closing.action != pencodeCloseNone || model.market != nil && model.market.open || state.KeyPressed(input.KeyEscape) || state.MousePressed(input.MouseRight) {
		active := drag.active
		*drag = pencodeFileDrag{suppressClick: drag.suppressClick}
		return active
	}
	pointer := state.Pointer()
	zoom := max(float32(model.settings.Zoom)/100, .01)
	x, y := pointer.X/zoom, pointer.Y/zoom
	if state.PointerPressed() {
		*drag = pencodeFileDrag{}
		// Use the press origin so a move coalesced into the same input frame
		// cannot turn the destination into the source.
		px, py := pointer.PressX/zoom, pointer.PressY/zoom
		if model.pointerInsideRefs(world, px, py, []string{"sidebar"}) {
			project, relative, _, _ := model.fileTreeTarget(world, px, py)
			if project != nil && relative != "" {
				drag.source, drag.path, drag.x, drag.y = project, relative, px, py
			}
		}
	}
	if drag.source == nil {
		return false
	}
	if model.projectIndex(drag.source.Path) < 0 || !pointer.Down && !pointer.Released {
		active := drag.active
		*drag = pencodeFileDrag{suppressClick: drag.suppressClick}
		return active
	}
	dx, dy := x-drag.x, y-drag.y
	if !drag.active && dx*dx+dy*dy < 36 {
		if pointer.Released {
			*drag = pencodeFileDrag{}
		}
		return false
	}
	changed := !drag.active
	drag.active, drag.suppressClick = true, true
	model.lastFileClick = 0
	if changed || x != drag.x || y != drag.y || pointer.Released || pointer.WheelY != 0 {
		drag.x, drag.y = x, y
		var target *pencodeProject
		directory, depth := "", 0
		if model.pointerInsideRefs(world, x, y, []string{"sidebar"}) {
			project, relative, isDir, rowDepth := model.fileTreeTarget(world, x, y)
			if project != nil {
				if !isDir {
					relative = path.Dir(relative)
					rowDepth--
				}
				if relative == "." {
					relative = ""
				}
				// The transfer also checks resolved filesystem paths at drop time.
				if project != drag.source || !pencodeResourceContains(drag.path, relative) {
					target, directory, depth = project, relative, rowDepth
				}
			}
		}
		if drag.target != target || drag.directory != directory {
			drag.target, drag.directory, drag.depth = target, directory, depth
			drag.expandAt = time.Time{}
			if target != nil && (target.Collapsed || directory != "" && !model.isProjectDirectoryExpanded(target, directory, depth)) {
				drag.expandAt = time.Now().Add(650 * time.Millisecond)
				if model.app != nil && model.app.WakeAt != nil {
					model.app.WakeAt(drag.expandAt)
				}
			}
			changed = true
		}
	}
	if pointer.Released {
		if drag.target != nil {
			if err := model.moveProjectResource(drag.source, drag.path, drag.target, drag.directory); err != nil {
				model.setProjectStatus(drag.target, err.Error())
			}
			setPencodeEditorValue(world, model.surface, model.activeContent())
		}
		*drag = pencodeFileDrag{suppressClick: true}
		return true
	}
	if drag.target != nil && !drag.expandAt.IsZero() && !time.Now().Before(drag.expandAt) {
		if drag.target.Collapsed {
			drag.target.Collapsed = false
		} else if !model.isProjectDirectoryExpanded(drag.target, drag.directory, drag.depth) {
			model.toggleProjectDirectory(drag.target, drag.directory, drag.depth)
		}
		drag.expandAt = time.Time{}
		return true
	}
	return changed
}
