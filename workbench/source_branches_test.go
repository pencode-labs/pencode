package workbench

import (
	"fmt"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	"math"
	"strings"
	"testing"

	"github.com/diiyw/onix/input"
)

func sourceBranchRemote(t *testing.T, r sourceRepository) {
	t.Helper()
	remote := sourceTestRepository(t)
	sourceTestWrite(t, remote, "resources/main.go", "package demo\n// remote\n")
	sourceTestGit(t, remote, "add", "--all")
	sourceTestGit(t, remote, "commit", "-m", "remote")
	sourceTestGit(t, remote, "branch", "feature/topic")
	sourceTestGit(t, r, "remote", "add", "origin", remote.Root)
	sourceTestGit(t, r, "fetch", "origin")
	sourceTestGit(t, r, "remote", "set-head", "origin", "main")
}

func TestSourceControlBranchSelection(t *testing.T) {
	r := sourceTestRepository(t)
	sourceTestGit(t, r, "commit", "--allow-empty", "-m", "local")
	sourceBranchRemote(t, r)
	branches, err := r.branches()
	if err != nil || len(branches) != 3 {
		t.Fatalf("branches=%+v err=%v", branches, err)
	}
	var remote sourceBranch
	for _, branch := range branches {
		if strings.HasSuffix(branch.Ref, "/HEAD") {
			t.Fatal("remote HEAD alias included")
		}
		if branch.Ref == "refs/remotes/origin/feature/topic" {
			remote = branch
		}
	}
	if !remote.Remote || remote.Name != "origin/feature/topic" {
		t.Fatalf("remote branch=%+v", remote)
	}
	if err := r.switchBranch(remote, branches); err != nil {
		t.Fatal(err)
	}
	if got := sourceTestGit(t, r, "symbolic-ref", "--short", "HEAD"); got != "feature/topic" {
		t.Fatalf("selected branch=%q", got)
	}
	if got := sourceTestGit(t, r, "rev-parse", "--symbolic-full-name", "@{upstream}"); got != remote.Ref {
		t.Fatalf("upstream=%q", got)
	}
	// Tracking can use a differently named local branch; select that branch again.
	sourceTestGit(t, r, "branch", "-m", "local-topic")
	sourceTestGit(t, r, "switch", "main")
	branches, err = r.branches()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.switchBranch(remote, branches); err != nil {
		t.Fatal(err)
	}
	if got := sourceTestGit(t, r, "symbolic-ref", "--short", "HEAD"); got != "local-topic" {
		t.Fatalf("tracking branch not reused: %q", got)
	}
	// A colliding unrelated branch must not be overwritten by remote selection.
	for _, branch := range branches {
		if branch.Ref == "refs/remotes/origin/main" && r.switchBranch(branch, branches) == nil {
			t.Fatal("unrelated local main was reused for a different remote branch")
		}
	}
	if sourceTestGit(t, r, "log", "main", "-1", "--format=%s") != "local" {
		t.Fatal("remote selection rewrote local main")
	}
}

func TestSourceControlBranchMenuInteraction(t *testing.T) {
	r := sourceTestRepository(t)
	sourceTestWrite(t, r, "resources/main.go", "package demo\n// local\n")
	sourceTestGit(t, r, "add", "--all")
	sourceTestGit(t, r, "commit", "-m", "base")
	sourceTestGit(t, r, "branch", "topic")
	sourceBranchRemote(t, r)
	t.Setenv("ONIX_PENCODE_PROFILE_PROJECT", r.Root)
	t.Setenv("ONIX_PENCODE_PROFILE_FILE", "resources/main.go")
	app, m, _, state := languageInteractionFixture(t)
	editorContextClick(t, app, m, state, "status-source-control")
	sourceWait(t, app, m)
	s := m.sourceState()
	editorContextClick(t, app, m, state, "sc-branches")
	if !s.BranchMenuOpen || s.Prompt != "" {
		t.Fatal("branch button did not open a menu directly")
	}
	menu := editorContextBounds(t, m, "sc-branch-menu")
	root := editorContextBounds(t, m, "pencode-root")
	if menu.Height < 80 || menu.X < root.X+11 || menu.X+menu.Width > root.X+root.Width-11 || menu.Y < root.Y+11 || menu.Y+menu.Height > root.Y+root.Height-11 {
		t.Fatalf("branch menu is outside the root: menu=%+v root=%+v", menu, root)
	}
	menuNode, _ := m.fontResources.HTMLNode(m.surface, "sc-branch-menu")
	if entity, ok := menuNode.ScrollContainer(); ok {
		if scroll, ok := module.ContentGet[scene.UIScroll](m.fontResources, entity); ok && scroll.ContentSize[1] > scroll.ViewportSize[1]+1 {
			t.Fatalf("short menu unnecessarily scrolls: content=%v viewport=%v", scroll.ContentSize, scroll.ViewportSize)
		}
	}
	lastRow := editorContextBounds(t, m, "sc-branch:"+s.Snapshot.Branches[len(s.Snapshot.Branches)-1].Ref)
	if lastRow.Y+lastRow.Height > menu.Y+menu.Height-5 {
		t.Fatalf("short menu clips its last branch: menu=%+v last=%+v", menu, lastRow)
	}
	editorContextClick(t, app, m, state, "sc-branch:refs/heads/topic")
	if !s.BranchMenuOpen || !s.BranchSubmenu || s.Done != nil || s.Snapshot.Branch != "main" {
		t.Fatal("branch row should open its submenu without switching")
	}
	editorContextClick(t, app, m, state, "sc-branch-checkout")
	sourceWait(t, app, m)
	if s.BranchMenuOpen || s.Snapshot.Branch != "topic" || s.Prompt != "" {
		t.Fatal("submenu checkout did not switch the local branch")
	}
	editorContextClick(t, app, m, state, "sc-branches")
	editorContextClick(t, app, m, state, "sc-branch:refs/remotes/origin/feature/topic")
	editorContextClick(t, app, m, state, "sc-branch-checkout")
	sourceWait(t, app, m)
	if s.Snapshot.Branch != "feature/topic" {
		t.Fatal("remote selection did not create a tracking branch")
	}
	press := func(key input.KeyCode) {
		t.Helper()
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: key})
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: key})
		languageInteractionStep(t, app)
	}
	editorContextClick(t, app, m, state, "sc-branches")
	press(input.KeyHome)
	press(input.KeyDown)
	press(input.KeyRight)
	press(input.KeyEnter)
	sourceWait(t, app, m)
	if s.Snapshot.Branch != "main" {
		t.Fatalf("keyboard selection failed: %s", s.Snapshot.Branch)
	}
	editorContextClick(t, app, m, state, "sc-branches")
	press(input.KeyRight)
	if !s.BranchSubmenu || s.BranchSubIndex != 1 {
		t.Fatal("current branch should focus Create Branch, skipping disabled Checkout")
	}
	press(input.KeyDown)
	if s.BranchSubIndex != 2 {
		t.Fatal("current branch should allow Create Tag after Create Branch")
	}
	press(input.KeyDown)
	if s.BranchSubIndex != 3 {
		t.Fatal("current branch should allow Rename after Create Tag")
	}
	press(input.KeyDown)
	if s.BranchSubIndex != 1 {
		t.Fatal("submenu navigation should skip disabled branch actions")
	}
	press(input.KeyEscape)
	if !s.BranchMenuOpen || s.BranchSubmenu {
		t.Fatal("Escape should first return from the submenu to the branch list")
	}
	press(input.KeyEscape)
	if s.BranchMenuOpen || m.bottom.active != pencodePanelSourceControl {
		t.Fatal("Escape did not close just the branch menu")
	}
	editorContextClick(t, app, m, state, "sc-branches")
	editorContextClick(t, app, m, state, "sc-message")
	if s.BranchMenuOpen {
		t.Fatal("outside click did not dismiss branch menu")
	}
	// Lists grow past the old 320px cap before scrolling at the window edge.
	for i := range 30 {
		sourceTestGit(t, r, "branch", fmt.Sprintf("topic-%02d", i))
	}
	m.refreshSourceControl()
	sourceWait(t, app, m)
	editorContextClick(t, app, m, state, "sc-branches")
	press(input.KeyEnd)
	languageInteractionStep(t, app)
	last := editorContextBounds(t, m, "sc-branch:"+s.Snapshot.Branches[len(s.Snapshot.Branches)-1].Ref)
	menu = editorContextBounds(t, m, "sc-branch-menu")
	// Bounds are layout coordinates; apply the actual scroll position.
	node, _ := m.fontResources.HTMLNode(m.surface, "sc-branch-menu")
	entity, ok := node.ScrollContainer()
	scroll, scrolled := module.ContentGet[scene.UIScroll](m.fontResources, entity)
	if !ok || !scrolled || !s.BranchMenuOpen || menu.Height <= 320 || scroll.Position[1] >= 0 || last.Y+scroll.Position[1] < menu.Y || last.Y+last.Height+scroll.Position[1] > menu.Y+menu.Height+1 {
		t.Fatal("long branch list did not create a scrollable menu")
	}
	press(input.KeyEscape)
}

func TestSourceControlBranchSubmenuCreation(t *testing.T) {
	r := sourceTestRepository(t)
	sourceTestWrite(t, r, "resources/main.go", "package demo\n// base\n")
	sourceTestGit(t, r, "add", "--all")
	sourceTestGit(t, r, "commit", "-m", "base")
	sourceTestGit(t, r, "branch", "topic")
	sourceTestGit(t, r, "commit", "--allow-empty", "-m", "current branch")
	sourceBranchRemote(t, r)
	t.Setenv("ONIX_PENCODE_PROFILE_PROJECT", r.Root)
	t.Setenv("ONIX_PENCODE_PROFILE_FILE", "resources/main.go")
	app, m, _, state := languageInteractionFixture(t)
	editorContextClick(t, app, m, state, "status-source-control")
	sourceWait(t, app, m)
	s := m.sourceState()
	for _, ref := range []string{"refs/heads/main", "refs/heads/topic", "refs/remotes/origin/feature/topic"} {
		base := sourceTestGit(t, r, "rev-parse", ref)
		current := s.Snapshot.Branch
		name := "new/" + strings.TrimPrefix(ref, "refs/")
		tagName := "tag/" + strings.TrimPrefix(ref, "refs/")
		currentHead := sourceTestGit(t, r, "rev-parse", "HEAD")
		editorContextClick(t, app, m, state, "sc-branches")
		editorContextClick(t, app, m, state, "sc-branch:"+ref)
		editorContextClick(t, app, m, state, "sc-branch-tag")
		if s.Prompt != "tag" || s.PromptHash != ref || s.PromptValue != "" || s.BranchMenuOpen || s.BranchSubmenu || s.Done != nil {
			t.Fatalf("tag prompt did not target %s", ref)
		}
		s.PromptValue = tagName
		editorContextClick(t, app, m, state, "sc-prompt-confirm")
		sourceWait(t, app, m)
		if sourceTestGit(t, r, "rev-parse", "refs/tags/"+tagName) != base {
			t.Fatalf("new tag %s did not point to %s", tagName, ref)
		}
		if s.Snapshot.Branch != current || sourceTestGit(t, r, "rev-parse", "HEAD") != currentHead {
			t.Fatal("tag creation changed the current branch or commit")
		}
		open := func() {
			t.Helper()
			editorContextClick(t, app, m, state, "sc-branches")
			editorContextClick(t, app, m, state, "sc-branch:"+ref)
			editorContextClick(t, app, m, state, "sc-branch-create")
			if s.Prompt != "branch" || s.PromptHash != ref || s.PromptValue != "" || s.BranchMenuOpen || s.BranchSubmenu || s.Done != nil {
				t.Fatalf("create prompt did not target %s", ref)
			}
		}
		open()
		s.PromptValue = name
		editorContextClick(t, app, m, state, "sc-prompt-cancel")
		if _, err := r.command("show-ref", "--verify", "refs/heads/"+name); err == nil || s.Snapshot.Branch != current {
			t.Fatal("cancel created or switched a branch")
		}
		open()
		editorContextClick(t, app, m, state, "sc-prompt-confirm")
		if s.Prompt != "branch" || s.Done != nil {
			t.Fatal("empty branch name should keep the prompt open")
		}
		s.PromptValue = name
		editorContextClick(t, app, m, state, "sc-prompt-confirm")
		sourceWait(t, app, m)
		if s.Snapshot.Branch != name || sourceTestGit(t, r, "rev-parse", "HEAD") != base {
			t.Fatalf("new branch %s did not start at %s", name, ref)
		}
		if _, ok := s.branch("refs/heads/" + name); !ok {
			t.Fatal("created branch missing from refreshed menu")
		}
	}
}

func TestSourceControlBranchMenuRenderCache(t *testing.T) {
	m := &workspaceModel{}
	s := m.sourceState()
	s.Snapshot.Branch = "main"
	s.Snapshot.Branches = []sourceBranch{{Ref: "refs/heads/main", Name: "main"}, {Ref: "refs/remotes/origin/topic", Name: "origin/topic", Remote: true}}
	m.toggleSourceBranches()
	render := func() {
		s.overlayView.reset()
		m.renderSourceBranches(&s.overlayView)
		m.renderSourceToolbar(&s.toolsView)
	}
	render()
	for _, submenu := range []bool{false, true} {
		s.BranchSubmenu = submenu
		render()
		if allocations := testing.AllocsPerRun(100, render); allocations != 0 {
			t.Fatalf("unchanged branch menu (submenu=%v) allocated %g times", submenu, allocations)
		}
	}
}

func TestSourceControlBranchDeletion(t *testing.T) {
	r := sourceTestRepository(t)
	sourceTestGit(t, r, "commit", "--allow-empty", "-m", "base")
	sourceTestGit(t, r, "branch", "merged/topic")
	local := sourceBranch{Ref: "refs/heads/merged/topic", Name: "merged/topic"}
	if err := r.deleteBranch(local); err != nil {
		t.Fatal(err)
	}
	if _, err := r.command("show-ref", "--verify", local.Ref); err == nil {
		t.Fatal("local branch still exists")
	}
	if err := r.deleteBranch(sourceBranch{Ref: "refs/heads/main", Name: "main"}); err == nil {
		t.Fatal("deleted the checked out branch")
	}
	sourceTestGit(t, r, "switch", "-c", "unmerged")
	sourceTestGit(t, r, "commit", "--allow-empty", "-m", "keep this commit")
	sourceTestGit(t, r, "switch", "main")
	if err := r.deleteBranch(sourceBranch{Ref: "refs/heads/unmerged", Name: "unmerged"}); err == nil {
		t.Fatal("deleted unmerged commits without consent")
	}

	remote := sourceTestRepository(t)
	sourceTestGit(t, remote, "commit", "--allow-empty", "-m", "remote base")
	sourceTestGit(t, remote, "branch", "feature/topic")
	// Match the longest remote name, including slashes, instead of splitting
	// origin/team/feature/topic at the first slash.
	sourceTestGit(t, r, "remote", "add", "origin", remote.Root)
	sourceTestGit(t, r, "remote", "add", "origin/team", remote.Root)
	sourceTestGit(t, r, "fetch", "origin/team")
	branch := sourceBranch{Ref: "refs/remotes/origin/team/feature/topic", Name: "origin/team/feature/topic", Remote: true}
	if err := r.deleteBranch(branch); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.command("show-ref", "--verify", "refs/heads/feature/topic"); err == nil {
		t.Fatal("deleted only the tracking ref, leaving the branch on the remote")
	}
	if _, err := r.command("show-ref", "--verify", branch.Ref); err == nil {
		t.Fatal("remote tracking ref still exists")
	}
	if _, err := r.command("show-ref", "--verify", "refs/heads/main"); err != nil {
		t.Fatal("remote deletion removed a local branch")
	}
}

func TestSourceControlBranchSubmenuDeletion(t *testing.T) {
	r := sourceTestRepository(t)
	sourceTestWrite(t, r, "resources/main.go", "package demo\n")
	sourceTestGit(t, r, "add", "--all")
	sourceTestGit(t, r, "commit", "-m", "base")
	sourceTestGit(t, r, "branch", "topic")
	t.Setenv("ONIX_PENCODE_PROFILE_PROJECT", r.Root)
	t.Setenv("ONIX_PENCODE_PROFILE_FILE", "resources/main.go")
	app, m, _, state := languageInteractionFixture(t)
	editorContextClick(t, app, m, state, "status-source-control")
	sourceWait(t, app, m)
	s := m.sourceState()
	open := func(ref string) {
		t.Helper()
		editorContextClick(t, app, m, state, "sc-branches")
		row := editorContextBounds(t, m, "sc-branch:"+ref)
		scale := float64(m.settings.Zoom) / 100
		x, y := float64(row.X+row.Width/2)*scale, float64(row.Y+row.Height/2)*scale
		state.Enqueue(input.Event{Kind: input.EventMouseMove, X: x, Y: y})
		languageInteractionStep(t, app)
		if !s.BranchSubmenu || !s.BranchMenuOpen || s.Snapshot.Branches[s.BranchIndex].Ref != ref || s.MenuOpen {
			t.Fatal("hover did not open the branch submenu")
		}
		menu := editorContextBounds(t, m, "sc-branch-menu")
		submenu := editorContextBounds(t, m, "sc-branch-submenu")
		root := editorContextBounds(t, m, "pencode-root")
		if submenu.X < root.X || submenu.X+submenu.Width > root.X+root.Width || submenu.Y < root.Y || submenu.Y+submenu.Height > root.Y+root.Height {
			t.Fatalf("submenu outside window: %+v root=%+v", submenu, root)
		}
		if math.Abs(float64(submenu.X-menu.X-menu.Width)) > 1 && math.Abs(float64(submenu.X+submenu.Width-menu.X)) > 1 {
			t.Fatalf("submenu does not touch parent: parent=%+v submenu=%+v", menu, submenu)
		}
		state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(submenu.X+submenu.Width/2) * scale, Y: float64(submenu.Y+20) * scale})
		languageInteractionStep(t, app)
		if !s.BranchSubmenu {
			t.Fatal("entering the submenu closed it")
		}
	}
	open("refs/heads/topic")
	editorContextClick(t, app, m, state, "sc-branch-delete")
	if s.Prompt != "delete-branch" || s.Done != nil {
		t.Fatal("branch deletion did not wait for confirmation")
	}
	editorContextClick(t, app, m, state, "sc-prompt-cancel")
	if _, err := r.command("show-ref", "--verify", "refs/heads/topic"); err != nil {
		t.Fatal("cancel deleted the branch")
	}
	open("refs/heads/topic")
	editorContextClick(t, app, m, state, "sc-branch-delete")
	editorContextClick(t, app, m, state, "sc-prompt-confirm")
	sourceWait(t, app, m)
	if _, ok := s.branch("refs/heads/topic"); ok {
		t.Fatal("deleted branch remained in the refreshed menu")
	}
	open("refs/heads/main")
	editorContextClick(t, app, m, state, "sc-branch-delete")
	if s.Prompt != "" || s.Done != nil || !s.BranchSubmenu || !s.BranchMenuOpen {
		t.Fatal("current branch deletion should be disabled")
	}
	row := editorContextBounds(t, m, "sc-branch:refs/heads/main")
	if allocations := testing.AllocsPerRun(100, func() {
		m.hoverSourceBranches(m.fontResources, row.X+row.Width/2, row.Y+row.Height/2)
		m.positionSourceBranchSubmenu(m.fontResources)
	}); allocations != 0 {
		t.Fatalf("unchanged submenu hover allocated %g times", allocations)
	}
	editorContextClick(t, app, m, state, "sc-branch-rename")
	if s.Prompt != "rename-branch" || s.PromptHash != "refs/heads/main" || s.PromptValue != "main" {
		t.Fatalf("rename prompt did not target the selected branch: %+v", s)
	}
	s.PromptValue = "renamed-main"
	editorContextClick(t, app, m, state, "sc-prompt-confirm")
	sourceWait(t, app, m)
	if s.Snapshot.Branch != "renamed-main" {
		t.Fatalf("rename did not refresh the current branch: %q", s.Snapshot.Branch)
	}
}

func TestSourceControlBranchRename(t *testing.T) {
	r := sourceTestRepository(t)
	sourceTestGit(t, r, "commit", "--allow-empty", "-m", "base")
	main := sourceBranch{Ref: "refs/heads/main", Name: "main"}
	if err := r.renameBranch(main, "renamed/current"); err != nil {
		t.Fatal(err)
	}
	if got := sourceTestGit(t, r, "symbolic-ref", "--short", "HEAD"); got != "renamed/current" {
		t.Fatalf("renamed current branch=%q", got)
	}
	sourceTestGit(t, r, "branch", "topic")
	topic := sourceBranch{Ref: "refs/heads/topic", Name: "topic"}
	for _, name := range []string{"renamed/current", "invalid name", "-option"} {
		if err := r.renameBranch(topic, name); err == nil {
			t.Fatalf("rename to %q should fail", name)
		}
	}
	if err := r.renameBranch(topic, "renamed/topic"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.command("show-ref", "--verify", "refs/heads/renamed/topic"); err != nil {
		t.Fatal(err)
	}
	if err := r.renameBranch(sourceBranch{Remote: true, Name: "origin/main"}, "remote"); err == nil {
		t.Fatal("remote-tracking refs must not be renamed as local branches")
	}
}
