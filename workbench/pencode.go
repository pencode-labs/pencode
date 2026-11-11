package workbench

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onix-engine/onix/ecs"
	"github.com/onix-engine/onix/graphics"
	"github.com/onix-engine/onix/graphics/codec/jpeg"
	ontex "github.com/onix-engine/onix/graphics/codec/ontex"
	"github.com/onix-engine/onix/graphics/codec/png"
	graphicssvg "github.com/onix-engine/onix/graphics/codec/svg"
	"github.com/onix-engine/onix/graphics/codec/webp"
	onixsvg "github.com/onix-engine/onix/images/svg"
	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/module"
	"github.com/onix-engine/onix/resource"
	sceneapi "github.com/onix-engine/onix/scene"
	"github.com/onix-engine/onix/syntax"
	"github.com/onix-engine/onix/text"
	"github.com/onix-engine/onix/text/piecetree"
	engineui "github.com/onix-engine/onix/ui"
	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/onix-engine/onix/window"
	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/localization"
	"github.com/pencode-labs/pencode/output"
	"github.com/pencode-labs/pencode/peb"
	"github.com/pencode-labs/pencode/protocol"
	"github.com/pencode-labs/pencode/textsearch"
)

const (
	pencodeTitlebarHeight    float32 = 40
	pencodeTitlebarInset     float32 = 1 // Root border.
	pencodeViewportWidth     float64 = 1440
	pencodeHighlightDebounce         = 50 * time.Millisecond
)

type pencodeScreen uint8

const (
	pencodeLauncher pencodeScreen = iota
	pencodeWorkspace
)

type treeEntry struct {
	Path     string
	Name     string
	Depth    int
	IsDir    bool
	Openable bool
}

type openFile struct {
	document        *client.Document
	customEditor    *extensions.Editor
	savedContent    piecetree.Snapshot
	contentSnapshot piecetree.Snapshot
	editorID        string
	Path            string
	Name            string
	pageLabel       string
	pencodeEditHistory
	editorView             pencodeEditorView
	editorGeneration       uint64
	saveRevision           uint64
	page                   protocol.DocumentPage
	pageLoading, pageDirty bool
	forcedText             bool // Preserve the explicit decoding choice when switching views.
	unsupported            bool // The file service did not provide a text document.
	editorSplit            bool
	externalChanged        bool
	Dirty                  bool
	Preview                bool
	highlightSlot          uint8 // Valid only while the bounded cache slot still owns this file.
}

type pencodeEdit struct {
	removed, inserted         string
	start, oldCaret, newCaret int
}

type pencodeProject struct {
	restoring               *pencodeProjectState
	debugBreakpoints        map[string][]pencodeBreakpoint
	watch                   *pencodeProjectWatch
	Files                   map[string]*openFile
	scan                    *pencodeProjectScan
	backend                 *client.Client
	ExpandedDirs            map[string]bool
	Name                    string
	PreviewPath             string
	historyKey              string
	ActivePath              string
	Path                    string
	Status                  string
	workspace               protocol.Workspace
	Tabs                    []string
	Entries                 []treeEntry
	tree                    pencodeTreeView
	sourceControl           pencodeSourceControl
	index                   uint64
	debugBreakpointRevision uint64
	Collapsed               bool
	AllDirectoriesCollapsed bool
	treePending             bool
}

type workspaceModel struct {
	locale                              localization.Pack
	languagePacks                       []pencodeLanguagePack
	languageOptions                     []htmlui.VNode
	highlightDue                        time.Time
	syntaxMemoryDue                     time.Time
	lastFileClickAt                     time.Time
	syntaxDefinitions                   *syntax.Definitions
	terminals                           *pencodeTerminals
	extensionImages                     map[string]pencodeExtensionImage
	hoverCancel                         context.CancelFunc
	hoverWorker                         *latestWorker[pencodeHoverJob, pencodeHoverResult]
	hoverDocument                       *client.Syntax
	highlightCancel                     context.CancelFunc
	highlightWorker                     *latestWorker[pencodeHighlightJob, pencodeHighlightResult]
	backend                             *client.Client
	files                               map[string]*openFile
	referenceWorker                     *latestWorker[pencodeReferenceJob, pencodeReferenceResult]
	output                              *output.Store
	highlightCache                      *pencodeHighlightCache
	expandedDirs                        map[string]bool
	ports                               *pencodePorts
	syntaxTheme                         *syntax.Theme
	revealProject                       *pencodeProject
	fileIcons                           *pencodeFileIcons
	quickWorker                         *latestWorker[pencodeQuickJob, pencodeQuickResult]
	languages                           *pencodeLanguageState
	workbenchTheme                      *pencodeTheme
	customDocuments                     map[*openFile]*pencodeCustomDocument
	referenceCancel                     context.CancelFunc
	findPattern                         *textsearch.Pattern
	session                             *Session
	market                              *pencodeMarketplace
	extensionStates                     map[string]pencodeExtensionState
	app                                 *module.Host
	fontResources                       *module.Content
	fonts                               map[string]pencodeFont
	defaultFont                         *text.Font
	fileIO                              *pencodeFileIO
	quickCancel                         context.CancelFunc
	extensionIcons                      map[string][]byte
	shortcutLabels                      [pencodeShortcutCount]string
	shortcutAriaLabels                  [pencodeShortcutCount]string
	quickMatches                        [8]string
	highlightedContent                  piecetree.Snapshot
	findContent                         piecetree.Snapshot
	findPreview                         *pencodeCustomDocument
	replaceQuery                        string
	shortcutQuery                       string
	defaultFontID                       string
	extensionFeedback                   string
	findQuery                           string
	findCachedQuery                     string
	settingsPath                        string
	activePath                          string
	defaultUIFontID                     string
	lineInfo                            string
	findCountLabel                      string
	projectName                         string
	selectionPrevious                   string
	revealPath                          string // pending reveal row, empty when nothing is pending
	captureAction                       string
	highlightedPath                     string
	keymapName                          string
	findError                           string
	settingsFeedback                    string
	zoomLabel                           string
	uiFontSizeLabel                     string
	fontSizeLabel                       string
	status                              string
	editorWidthLabel                    string
	editorStyle                         string
	settingsExtension                   string
	themeSourceCSS                      string
	lastDirectory                       string
	quickProjectPath                    string
	sidebarWidthStyle                   string
	sourceChangesWidthStyle             string
	quickCachedQuery                    string
	projectPath                         string
	quickQuery                          string
	editorHeader                        pencodeEditorHeader
	fileClipboard                       pencodeFileClipboard
	navigation                          pencodeNavigationHistory
	highlightRebaseScratch              []sceneapi.TextSpan2D
	entries                             []treeEntry
	findMatches                         []textsearch.Match
	iconThemeOptions                    []htmlui.VNode
	fileIconThemes                      []*pencodeFileIcons
	themeOptions                        []htmlui.VNode
	themes                              []pencodeTheme
	uiFontOptions                       []htmlui.VNode
	customEditors                       []*extensions.Editor
	projects                            []*pencodeProject
	highlightScratch                    []sceneapi.TextSpan2D
	findScope, findCachedScope          pencodeFindScope
	extensionSettings                   []*pencodeExtensionSettings
	debugTemplates                      []pencodeDebugTemplate
	debugTemplateRows                   []pencodeDebugRow
	tabs                                []string
	codeFontOptions                     []htmlui.VNode
	tabScroll                           pencodeTabScroll
	tooltip                             pencodeTooltip
	tabContext                          pencodeTabContext
	highlightRequest                    pencodeHighlightJob
	settings                            pencodeSettings
	fileContext                         pencodeFileContext
	fileDrag                            pencodeFileDrag
	bottom                              pencodeBottomPanel
	closing                             pencodeCloseRequest
	fileClose                           pencodeCloseRequest
	customClose                         pencodeCloseRequest
	portUI                              pencodePortUI
	inputContext                        pencodeInputContext
	terminalView                        pencodeTerminalView
	folderSearch                        pencodeFolderSearch
	actions                             pencodeActionState
	debug                               pencodeDebugState
	languageUI                          pencodeLanguageInteraction
	editor                              pencodeEditor
	sourceControl                       pencodeSourceControl
	characterCount                      int
	activeProjectIndex                  int
	findRevision                        uint64
	surface                             ecs.Entity
	editorGeneration                    uint64
	semanticPaintRevision               uint64
	extensionImageRevision              uint64
	lineCount                           int
	findIndex                           int
	referenceRevision, referenceApplied uint64
	windowTitlebarHeight                float64
	quickRevision                       uint64
	quickSnapshot                       uint64
	quickApplied                        uint64
	lastFileClick                       ecs.Entity
	highlightRevision                   uint64
	quickTargetLine                     int
	windowBorder                        ecs.Entity
	hoverRevision, hoverApplied         uint64
	highlightPaintRevision              uint64
	quickCount                          int
	quickIndex                          int
	root                                ecs.Entity
	resize                              pencodeWorkbenchResize
	viewportHeight                      float32
	viewportWidth                       float32
	findOptions                         textsearch.Options
	findCachedOptions                   textsearch.Options
	screen                              pencodeScreen
	findOpen                            bool
	allDirectoriesCollapsed             bool
	replaceOpen                         bool
	findNeedsFocus                      bool
	settingsShortcuts                   bool
	settingsAbout                       bool
	highlighted                         bool
	settingsOpen                        bool
	highlightPending                    bool
	settingsRemote                      bool
	settingsExtensions                  bool
	quickFocusEditor                    bool
	quickNeedsFocus                     bool
	quickOpen                           bool
	refresh                             pencodeRefresh
	settingsExtensionsExpanded          bool
	findSelection                       bool
	sidebarHidden                       bool
	capturingShortcut                   bool
	fullRefresh                         bool
}

type appProps struct {
	Model  *workspaceModel
	Screen pencodeScreen
}

type appState struct {
	Revision uint64
}

type treeProps struct {
	Model     *workspaceModel
	Theme     *pencodeTheme
	Icons     *pencodeFileIcons
	Project   *pencodeProject
	Path      string
	RowID     string
	TreeID    string
	Name      string
	Depth     int
	IsDir     bool
	Openable  bool
	HasErrors bool
	Editing   bool
}

type tabProps struct {
	Model     *workspaceModel
	Surface   *pencodeExtensionSurface
	Theme     *pencodeTheme
	Icons     *pencodeFileIcons
	Project   *pencodeProject
	Path      string
	Name      string
	Icon      string
	Active    bool
	Preview   bool
	Dirty     bool
	Busy      bool
	Unread    bool
	HasErrors bool
}

type projectProps struct {
	Model             *workspaceModel
	Theme             *pencodeTheme
	Icons             *pencodeFileIcons
	Project           *pencodeProject
	TreeID, SummaryID string
	ProblemRevision   uint64
}

type pencodeDefinitions struct {
	action  *htmlui.Definition[pencodeActionProps, struct{}]
	root    *htmlui.Definition[appProps, appState]
	tree    *htmlui.Definition[treeProps, struct{}]
	project *htmlui.Definition[projectProps, struct{}]
	tab     *htmlui.Definition[tabProps, struct{}]
	market  *htmlui.Definition[pencodeMarketProps, struct{}]
	connect *htmlui.Definition[pencodeConnectionProps, pencodeConnectionState]
}

func spawnPencodeSurface(world *module.Content, font, boldFont *text.Font, fontSize int) ecs.Entity {
	html := htmlui.New("")
	html.Font, html.BoldFont, html.FontSize, html.PassThrough = font, boldFont, float64(fontSize), false
	bundle := ecs.NewBundle(7)
	bundle.Put(sceneapi.UITransformFromXY(0, 0))
	bundle.Put(sceneapi.UIGlobalTransform{})
	bundle.Put(sceneapi.Visibility{Visible: true})
	bundle.Put(sceneapi.AnchorTopLeft)
	bundle.Put(sceneapi.UIStackIndex(10000))
	bundle.Put(sceneapi.Size2D{})
	bundle.Put(html)
	return world.Spawn(bundle)
}

func spawnPencodeWindowBorder(world *module.Content, color graphics.ColorRGBA) ecs.Entity {
	bundle := ecs.NewBundle(7)
	bundle.Put(sceneapi.UITransformFromXY(0, 0))
	bundle.Put(sceneapi.UIGlobalTransform{})
	bundle.Put(sceneapi.Visibility{Visible: true})
	bundle.Put(sceneapi.AnchorTopLeft)
	bundle.Put(sceneapi.UIStackIndex(1 << 30))
	bundle.Put(sceneapi.Size2D{})
	// Keep the window outline outside the HTML zoom and overflow hierarchy.
	// Match the surface's corner radius so the antialiased edges stay aligned.
	bundle.Put(sceneapi.NewUIBorder2D(sceneapi.OutlinedRoundedRectShape2D(
		graphics.ColorRGBA{}, color, pencodeWindowBorderRadius(1), 1)))
	return world.Spawn(bundle)
}

func pencodeWindowBorderRadius(pixelRatio float32) float32 {
	if runtime.GOOS == "windows" {
		return 12 * pixelRatio
	}
	return 12
}

func seedPencodeViewport(world *module.Content, app *module.Host) {
	if world == nil || app == nil || app.Context == nil {
		return
	}
	width, height := app.GetViewport()
	if width <= 0 || height <= 0 {
		width, height = app.Context.Width, app.Context.Height
	}
	if width <= 0 || height <= 0 {
		return
	}
	world.Store(sceneapi.ViewportSize{Width: float32(width), Height: float32(height), ScaleFactor: 1})
}

func newPencodeDefinitions(content *module.Content, page string) (*pencodeDefinitions, error) {
	page, translations, err := localizedPencodeTemplate(page)
	if err != nil {
		return nil, err
	}
	definitions := &pencodeDefinitions{}
	definitions.tree, err = content.Define[treeProps, struct{}](
		htmlui.NewTemplate(`<button data-ref="row" class="pencode-tree-row" type="button"><img data-ref="arrow" class="pencode-tree-arrow" src="icons/chevron-right.svg" alt=""><img data-ref="icon" class="pencode-tree-icon" src="icons/file-text.svg" alt=""><span data-ref="name" class="pencode-tree-name"></span></button>`), "",
		module.ContentBehavior[treeProps, struct{}]{
			Render: func(context module.ContentRenderContext[treeProps, struct{}], view *htmlui.View) {
				props := context.Props
				props.RowID = props.Model.projectTreeRowID(props.Project, props.Path)
				view.Attr("row", "id", props.RowID)
				view.Attr("row", "style", fmt.Sprintf("padding-left:%dpx", 12+props.Depth*16))
				view.Attr("row", "class", treeRowClass(props))
				if props.IsDir {
					view.Attr("row", "aria-expanded", boolAttribute(props.Model.isProjectDirectoryExpanded(props.Project, props.Path, props.Depth)))
					view.Attr("arrow", "src", props.Model.themedIcon("icons/chevron-right.svg", "icon.foreground"))
				} else {
					view.RemoveAttr("row", "aria-expanded")
				}
				view.Attr("icon", "src", props.Model.themedIcon(treeIcon(props), treeIconForeground(props)))
				view.Text("name", props.Name)
				nameClass := "pencode-tree-name"
				if props.HasErrors {
					nameClass = "pencode-tree-name pencode-error-name"
				}
				view.Attr("name", "class", nameClass)
				view.Event("row", htmlui.EventClick)
			},
			HandleEvent: handleTreeEvent,
		},
	)
	if err != nil {
		return nil, err
	}
	creationDefinition, err := content.Define[treeProps, struct{}](
		htmlui.NewTemplate(`<div id="file-tree-create" data-ref="create" class="pencode-tree-create"><img data-ref="create-icon" class="pencode-tree-icon" src="icons/file-text.svg" alt=""><div class="pencode-tree-input"><input id="file-tree-name" data-ref="create-name" type="text" class="pencode-tree-name-input" autocomplete="off" aria-describedby="file-tree-error"><div id="file-tree-error" data-ref="create-error" class="pencode-tree-create-error" role="alert" hidden></div></div></div>`),
		"",
		module.ContentBehavior[treeProps, struct{}]{
			Render: func(context module.ContentRenderContext[treeProps, struct{}], view *htmlui.View) {
				context.Props.Model.renderFileCreation(view)
			},
			HandleEvent: handleTreeEvent,
		},
	)
	if err != nil {
		return nil, err
	}
	definitions.project, err = content.Define[projectProps, struct{}](
		htmlui.NewTemplate(`<section data-ref="project" class="pencode-project"><div data-ref="summary" class="pencode-project-summary"><button data-ref="toggle" class="pencode-project-toggle" type="button"><img data-ref="arrow" class="pencode-project-arrow" src="icons/chevron-right.svg" alt=""><span data-ref="name" class="pencode-project-name"></span></button><button data-ref="close" class="pencode-project-close" type="button" title="关闭项目" aria-label="关闭项目"><img data-ref="close-icon" class="pencode-close-icon" src="icons/close.svg" alt=""></button></div><nav data-ref="tree" class="pencode-tree" aria-label="项目目录"><div data-ref="tree-height" class="pencode-tree-height"><div data-ref="tree-window" class="pencode-tree-window"></div></div></nav></section>`),
		"",
		module.ContentBehavior[projectProps, struct{}]{
			Render: func(context module.ContentRenderContext[projectProps, struct{}], view *htmlui.View) {
				props := context.Props
				if props == nil || props.Model == nil || props.Project == nil {
					return
				}
				view.Attr("close", "title", props.Model.tr("关闭项目"))
				view.Attr("close", "aria-label", props.Model.tr("关闭项目"))
				view.Attr("tree", "aria-label", props.Model.tr("项目目录"))
				className := "pencode-project"
				if props.Model.activeProjectState() == props.Project {
					className += " active"
				}
				if !props.Project.Collapsed {
					className += " expanded"
				}
				view.Attr("project", "class", className)
				summaryClass := "pencode-project-summary"
				if props.Model.fileDrag.isTarget(props.Project, "") {
					summaryClass += " drop-target"
				}
				view.Attr("summary", "class", summaryClass)
				props.TreeID = treeRowID(props.Project.Path)
				props.SummaryID = props.TreeID + "-summary"
				view.Attr("summary", "id", props.SummaryID)
				view.Attr("arrow", "src", props.Model.themedIcon("icons/chevron-right.svg", "icon.foreground"))
				view.Attr("close-icon", "src", props.Model.themedIcon("icons/close.svg", "icon.foreground"))
				view.Attr("tree", "id", props.TreeID)
				view.Attr("tree", "aria-busy", boolAttribute(props.Project.scan != nil))
				view.Attr("toggle", "aria-expanded", boolAttribute(!props.Project.Collapsed))
				view.Attr("close", "id", props.TreeID+"-close")
				view.Attr("close", "aria-label", "关闭项目 "+props.Project.Name)
				view.Text("name", props.Project.Name)
				if props.Project.Collapsed {
					view.Attr("tree", "hidden", "")
				} else {
					view.RemoveAttr("tree", "hidden")
				}
				view.Event("toggle", htmlui.EventClick)
				view.Event("close", htmlui.EventClick)
				if props.Project.Collapsed {
					view.Components("tree-window", nil)
					return
				}
				renderPencodeTree(context, view, definitions.tree, creationDefinition)
			},
			HandleEvent: handleProjectEvent,
		},
	)
	if err != nil {
		return nil, err
	}
	definitions.tab, err = content.Define[tabProps, struct{}](
		htmlui.NewTemplate(`<div data-ref="tab" class="pencode-tab"><button data-ref="select" class="pencode-tab-select" type="button" role="tab"><img data-ref="icon" class="pencode-tab-icon" src="icons/file-text.svg" alt=""><span data-ref="name" class="pencode-tab-name"></span></button><button data-ref="close" class="pencode-tab-close" type="button" title="关闭文件" aria-label="关闭文件"><span data-ref="dot" class="pencode-tab-dot" aria-hidden="true"></span></button></div>`),
		"",
		module.ContentBehavior[tabProps, struct{}]{
			Render: func(context module.ContentRenderContext[tabProps, struct{}], view *htmlui.View) {
				props := context.Props
				className := "pencode-tab"
				if props.Active {
					className += " active"
					view.Attr("tab", "id", "active-tab")
				} else {
					view.Attr("tab", "id", "file-tab:"+props.Path)
				}
				if props.Preview {
					className += " preview"
				}
				if props.Dirty {
					className += " dirty"
					view.RemoveAttr("dot", "hidden")
					view.Attr("select", "aria-label", props.Name+props.Model.tr("（未保存）"))
					view.Attr("close", "title", props.Model.tr("未保存，点击关闭"))
				} else {
					view.Attr("dot", "hidden", "")
					view.Attr("select", "aria-label", props.Name)
					view.Attr("close", "title", props.Model.tr("关闭文件"))
				}
				closeID := props.Path
				if props.Project != nil {
					closeID = props.Project.Path + ":" + closeID
				}
				view.Attr("close", "id", "file-tab-close:"+closeID)
				view.Attr("tab", "class", className)
				iconForeground := "tab.inactiveForeground"
				if props.Active {
					iconForeground = "tab.activeForeground"
				}
				icon := props.Icon
				if icon == "" {
					icon = props.Icons.source(props.Name, false)
				}
				if icon == "" {
					icon = "icons/file-text.svg"
				}
				view.Attr("icon", "src", props.Model.themedIcon(icon, iconForeground))
				if props.Unread {
					view.Attr("select", "aria-label", props.Name+props.Model.tr("（已完成，尚未查看）"))
				}
				if props.Busy {
					view.Attr("close", "title", props.Model.tr("关闭标签页并在后台运行"))
				}
				renderTabIndicator(view, props, iconForeground)
				view.Attr("tab", "aria-busy", boolAttribute(props.Busy))
				view.Attr("select", "aria-selected", boolAttribute(props.Active))
				view.Attr("close", "aria-label", props.Model.tr("关闭 ")+props.Name)
				view.Text("name", props.Name)
				nameClass := "pencode-tab-name"
				if props.HasErrors {
					nameClass = "pencode-tab-name pencode-error-name"
				}
				view.Attr("name", "class", nameClass)
				view.Event("select", htmlui.EventClick)
				view.Event("close", htmlui.EventClick)
			},
			HandleEvent: handleTabEvent,
		},
	)
	if err != nil {
		return nil, err
	}
	definitions.market, err = content.Define[pencodeMarketProps, struct{}](
		htmlui.NewTemplate(`<button data-ref="item" class="pencode-market-item" type="button" aria-pressed="false"><span data-ref="name" class="pencode-market-item-name"></span><span data-ref="description" class="pencode-market-item-description"></span></button>`), "",
		module.ContentBehavior[pencodeMarketProps, struct{}]{Render: renderPencodeMarketItem, HandleEvent: handlePencodeMarketItem})
	if err != nil {
		return nil, err
	}
	definitions.connect, err = content.Define[pencodeConnectionProps, pencodeConnectionState](
		htmlui.NewTemplate(`<article data-ref="row" class="pencode-connection-row" role="listitem"><div class="pencode-connection-copy"><span data-ref="dot" class="pencode-connection-dot"></span><strong data-ref="name"></strong><span data-ref="host"></span></div><button data-ref="project" class="pencode-icon-button" type="button" title="打开远端项目" aria-label="打开远端项目"><img data-ref="project-icon" src="icons/folder-opened.svg" alt=""></button><button data-ref="more" class="pencode-icon-button" type="button" title="更多操作" aria-label="更多操作" aria-haspopup="menu" aria-expanded="false"><img data-ref="more-icon" src="icons/ellipsis.svg" alt=""></button></article>`), "",
		module.ContentBehavior[pencodeConnectionProps, pencodeConnectionState]{Render: renderPencodeConnection, HandleEvent: handlePencodeConnection})
	if err != nil {
		return nil, err
	}
	definitions.action, err = content.Define[pencodeActionProps, struct{}](
		htmlui.NewTemplate(`<button data-ref="run" type="button" class="pencode-action"><img data-ref="icon" alt=""><span data-ref="text"></span></button>`), "",
		module.ContentBehavior[pencodeActionProps, struct{}]{Render: renderPencodeAction, HandleEvent: handlePencodeAction})
	if err != nil {
		return nil, err
	}
	shortcutRow, err := newShortcutDefinition(content)
	if err != nil {
		return nil, err
	}
	settingNav, err := newSettingNavDefinition(content)
	if err != nil {
		return nil, err
	}
	settingRow, err := newSettingDefinition(content)
	if err != nil {
		return nil, err
	}
	problemDefinition, err := newProblemGroupDefinition(content)
	if err != nil {
		return nil, err
	}
	portDefinition, err := newPortDefinition(content)
	if err != nil {
		return nil, err
	}
	debugDefinition, err := newDebugRowDefinition(content)
	if err != nil {
		return nil, err
	}
	directoryDefinition, err := newProjectDirectoryDefinition(content)
	if err != nil {
		return nil, err
	}
	recentDefinition, err := newRecentProjectDefinition(content)
	if err != nil {
		return nil, err
	}
	inputContextDefinition, err := newInputContextDefinition()
	if err != nil {
		return nil, err
	}
	definitions.root, err = content.Define[appProps, appState](
		tooltipTemplate(page, ""),
		"",
		module.ContentBehavior[appProps, appState]{
			Render: func(context module.ContentRenderContext[appProps, appState], view *htmlui.View) {
				if context.Props != nil && context.Props.Model != nil && context.Props.Model.patchWorkbench(context, view) {
					return
				}
				if context.Props != nil && context.Props.Model != nil {
					renderPencodeTranslations(context.Props.Model, view, translations)
				}
				renderPencodeRoot(context, view, definitions.project, definitions.tab, definitions.market, definitions.connect, definitions.action)
				if context.Props != nil && context.Props.Model != nil && context.Props.Screen == pencodeLauncher {
					context.Props.Model.renderRecentProjects(view, recentDefinition)
				}
				if context.Props != nil && context.Props.Model != nil {
					context.Props.Model.renderExtensionSettings(view, settingNav, settingRow)
					context.Props.Model.renderShortcutSettings(view, shortcutRow)
					context.Props.Model.renderUpdates(view)
				}
				if context.Props != nil && context.Props.Model != nil {
					context.Props.Model.renderInputContext(view, inputContextDefinition)
				}
				if context.Props != nil && context.Props.Model != nil {
					context.Props.Model.renderProjectPicker(view, directoryDefinition)
				}
				if context.Props != nil && context.Props.Model != nil && context.Props.Screen == pencodeWorkspace {
					context.Props.Model.renderBottomPanel(view, problemDefinition)
					context.Props.Model.renderPorts(view, portDefinition)
					context.Props.Model.renderDebugger(view, debugDefinition)
				}
			},
			HandleEvent: handlePencodeRootEvent,
			Unmount: func(context *module.ContentComponentContext[appProps, appState]) {
				if model := context.Props.Model; model != nil {
					model.editor.dispose(context.World)
				}
			},
		},
	)
	if err != nil {
		return nil, err
	}
	return definitions, nil
}

func renderPencodeRoot(context module.ContentRenderContext[appProps, appState], view *htmlui.View, projectDefinition *htmlui.Definition[projectProps, struct{}], tabDefinition *htmlui.Definition[tabProps, struct{}], marketDefinition *htmlui.Definition[pencodeMarketProps, struct{}], connectionDefinition *htmlui.Definition[pencodeConnectionProps, pencodeConnectionState], actionDefinition *htmlui.Definition[pencodeActionProps, struct{}]) {
	if context.Props == nil || context.Props.Model == nil {
		return
	}
	model := context.Props.Model
	model.ensureSettings()
	model.syncWorkbenchTheme(context.World)
	model.syncActions()
	model.syncProblems()
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		view.Attr("titlebar", "class", "pencode-titlebar native-controls")
		view.Attr("window-controls", "hidden", "")
	}
	for _, icon := range pencodeThemeIcons {
		view.Attr(icon.ref, "src", model.themedIcon(icon.source, icon.foreground))
	}
	model.renderTooltip(view)
	model.renderWorkbenchResize(view)
	model.renderLanguageTools(view)
	model.renderLanguageEditorPopups(view)
	model.renderCloseDialog(view)
	model.renderFileContext(view)
	model.renderFileDrag(view)
	model.renderTabContext(view)
	model.renderFolderSearch(view)
	model.renderActions(view, actionDefinition)
	model.renderExtensionUI(view)
	model.renderMarketplace(view, marketDefinition)
	model.renderConnection(view, connectionDefinition)
	view.Event("open-quick-search", htmlui.EventClick)
	view.Event("quick-open-query", htmlui.EventClick)
	view.Event("quick-open-query", htmlui.EventFocus)
	view.Text("quick-search-shortcut", model.shortcutLabels[pencodeShortcutQuickOpen])
	view.Attr("quick-open-query", "aria-expanded", boolAttribute(model.quickOpen))
	view.Attr("quick-open-query", "value", model.quickQuery)
	if model.activeProjectState() == nil && len(model.entries) == 0 {
		view.Attr("quick-open-query", "disabled", "")
		view.Attr("open-quick-search", "title", model.tr("请先打开项目目录"))
	} else {
		view.RemoveAttr("quick-open-query", "disabled")
		view.Attr("open-quick-search", "title", model.shortcutAriaLabels[pencodeShortcutQuickOpen])
	}
	view.Event("window-minimize", htmlui.EventClick)
	view.Event("window-maximize", htmlui.EventClick)
	view.Event("window-close", htmlui.EventClick)
	view.Event("open-project", htmlui.EventClick)
	view.Event("change-project", htmlui.EventClick)
	view.Event("collapse-directories", htmlui.EventClick)
	view.Event("reveal-active-file", htmlui.EventClick)
	view.Event("reload-from-disk", htmlui.EventClick)
	view.Event("editor", htmlui.EventInput)
	view.Event("editor-page-position", htmlui.EventChange)
	for _, ref := range [...]string{"editor-page-first", "editor-page-prev", "editor-page-next", "editor-page-last"} {
		view.Event(ref, htmlui.EventClick)
	}
	view.Event("open-settings", htmlui.EventClick)
	view.Event("close-settings", htmlui.EventClick)
	view.Event("settings-appearance-nav", htmlui.EventClick)
	view.Event("settings-shortcuts-nav", htmlui.EventClick)
	view.Event("font-decrease", htmlui.EventClick)
	view.Event("font-increase", htmlui.EventClick)
	view.Event("editor-width-decrease", htmlui.EventClick)
	view.Event("editor-width-increase", htmlui.EventClick)
	view.Event("ui-font-decrease", htmlui.EventClick)
	view.Event("ui-font-increase", htmlui.EventClick)
	view.Event("code-font-select", htmlui.EventChange)
	view.Event("ui-font-select", htmlui.EventChange)
	view.Event("theme-select", htmlui.EventChange)
	view.Event("icon-theme-select", htmlui.EventChange)
	view.Event("zoom-decrease", htmlui.EventClick)
	view.Event("zoom-increase", htmlui.EventClick)
	view.Event("reset-shortcuts", htmlui.EventClick)
	if model.findOpen && model.activeFile() != nil {
		model.refreshFindMatches()
	}
	view.Event("find-query", htmlui.EventInput)
	view.Event("replace-query", htmlui.EventInput)
	view.Event("find-previous", htmlui.EventClick)
	view.Event("find-next", htmlui.EventClick)
	view.Event("find-close", htmlui.EventClick)
	view.Event("replace-one", htmlui.EventClick)
	view.Event("replace-all", htmlui.EventClick)
	view.Event("find-toggle-replace", htmlui.EventClick)
	view.Event("find-selection", htmlui.EventClick)
	view.Event("find-preserve", htmlui.EventClick)
	renderFindToggle(view, "find-selection", model.findSelection)
	renderFindToggle(view, "find-preserve", model.findOptions.PreserveCase)
	renderSearchOptions(view, pencodeFindOptionRefs, model.findOptions)
	view.Attr("find-query", "value", model.findQuery)
	view.Attr("replace-query", "value", model.replaceQuery)
	renderReplaceToggle(view, "find-toggle-replace", model.replaceOpen)
	for _, ref := range [...]string{"find-toggle-replace", "find-selection"} {
		if model.renderedFind() != nil {
			view.Attr(ref, "hidden", "")
		} else {
			view.RemoveAttr(ref, "hidden")
		}
	}
	view.Text("find-error", model.findError)
	if model.findError == "" {
		view.Attr("find-error", "hidden", "")
	} else {
		view.RemoveAttr("find-error", "hidden")
	}
	for _, ref := range [...]string{"find-previous", "find-next", "replace-one", "replace-all"} {
		if len(model.findMatches) == 0 {
			view.Attr(ref, "disabled", "")
		} else {
			view.RemoveAttr(ref, "disabled")
		}
	}
	view.Event("quick-open-query", htmlui.EventInput)
	for _, ref := range pencodeQuickRowRefs {
		view.Event(ref, htmlui.EventClick)
	}
	view.Attr("editor", "style", model.editorStyle)
	view.Keyed("theme-select", model.themeOptions)
	view.Keyed("icon-theme-select", model.iconThemeOptions)
	view.Text("extensions-feedback", model.extensionFeedback)
	if model.extensionFeedback == "" {
		view.Attr("extensions-feedback", "hidden", "")
	} else {
		view.RemoveAttr("extensions-feedback", "hidden")
	}
	view.Text("editor-width-value", model.editorWidthLabel)
	view.Text("font-size-value", model.fontSizeLabel)
	view.Text("ui-font-size-value", model.uiFontSizeLabel)
	view.Keyed("code-font-select", model.codeFontOptions)
	view.Keyed("ui-font-select", model.uiFontOptions)
	view.Text("zoom-value", model.zoomLabel)
	view.Text("settings-feedback", model.settingsFeedback)
	if model.settingsFeedback == "" {
		view.Attr("settings-feedback", "hidden", "")
	} else {
		view.RemoveAttr("settings-feedback", "hidden")
	}
	if model.settingsShortcuts {
		view.Text("settings-page-title", model.tr("快捷键"))
		view.Attr("settings-appearance-nav", "class", "pencode-settings-nav-item")
		view.RemoveAttr("settings-appearance-nav", "aria-current")
		view.Attr("settings-shortcuts-nav", "class", "pencode-settings-nav-item active")
		view.Attr("settings-shortcuts-nav", "aria-current", "page")
		view.Attr("settings-appearance-pane", "hidden", "")
		view.RemoveAttr("settings-shortcuts-pane", "hidden")
		view.RemoveAttr("reset-shortcuts", "hidden")
	} else {
		view.Text("settings-page-title", model.tr("外观"))
		view.Attr("settings-appearance-nav", "class", "pencode-settings-nav-item active")
		view.Attr("settings-appearance-nav", "aria-current", "page")
		view.Attr("settings-shortcuts-nav", "class", "pencode-settings-nav-item")
		view.RemoveAttr("settings-shortcuts-nav", "aria-current")
		view.RemoveAttr("settings-appearance-pane", "hidden")
		view.Attr("settings-shortcuts-pane", "hidden", "")
		view.Attr("reset-shortcuts", "hidden", "")
	}
	if model.findOpen && model.activeFile() != nil {
		view.RemoveAttr("find-bar", "hidden")
	} else {
		view.Attr("find-bar", "hidden", "")
	}
	if model.replaceOpen {
		view.RemoveAttr("replace-row", "hidden")
	} else {
		view.Attr("replace-row", "hidden", "")
	}
	view.Text("find-count", model.findCountLabel)
	if model.quickOpen {
		model.refreshQuickMatches()
		view.RemoveAttr("quick-open", "hidden")
	} else {
		view.Attr("quick-open", "hidden", "")
	}
	if model.quickOpen && model.quickCount == 0 {
		view.RemoveAttr("quick-empty", "hidden")
	} else {
		view.Attr("quick-empty", "hidden", "")
	}
	if model.quickOpen && model.quickCount > 0 {
		view.Attr("quick-open-query", "aria-activedescendant", pencodeQuickRowRefs[model.quickIndex])
	} else {
		view.RemoveAttr("quick-open-query", "aria-activedescendant")
	}
	for index, ref := range pencodeQuickRowRefs {
		if model.quickOpen && index < model.quickCount {
			view.RemoveAttr(ref, "hidden")
			path := model.quickMatches[index]
			name, directory := path, model.quickProjectPath
			if slash := strings.LastIndexByte(path, '/'); slash >= 0 {
				name, directory = path[slash+1:], path[:slash]
			}
			view.Text(pencodeQuickLabelRefs[index], name)
			view.Text(pencodeQuickPathRefs[index], directory)
			view.Attr(ref, "title", path)
			className := ""
			foreground := "icon.foreground"
			if index == model.quickIndex {
				className = "active"
				foreground = "list.activeSelectionForeground"
			}
			icon := treeIcon(&treeProps{Model: model, Name: name})
			view.Attr(pencodeQuickIconRefs[index], "src", model.themedIcon(icon, foreground))
			view.Attr(ref, "class", className)
			view.Attr(ref, "aria-selected", boolAttribute(index == model.quickIndex))
		} else {
			view.Attr(ref, "hidden", "")
		}
	}

	if model.settingsOpen {
		view.RemoveAttr("settings-panel", "hidden")
		view.Attr("open-settings", "aria-expanded", "true")
	} else {
		view.Attr("settings-panel", "hidden", "")
		view.Attr("open-settings", "aria-expanded", "false")
	}
	if model.sidebarHidden {
		view.Attr("sidebar", "hidden", "")
		view.Attr("sidebar-resize", "hidden", "")
	} else {
		view.RemoveAttr("sidebar", "hidden")
		view.RemoveAttr("sidebar-resize", "hidden")
		if model.sidebarWidthStyle == "" {
			view.RemoveAttr("sidebar", "style")
		} else {
			view.Attr("sidebar", "style", model.sidebarWidthStyle)
		}
	}

	if context.Props.Screen == pencodeWorkspace {
		renderPencodeHighlight(model)
	}
	model.renderCustomEditor(view)
	view.Text("launcher-status", model.status)
	view.Text("line-info", model.lineInfo)
	view.Text("caret-info", model.editor.caretInfo)

	if context.Props.Screen == pencodeWorkspace {
		view.RemoveAttr("change-project", "hidden")
	} else {
		view.Attr("change-project", "hidden", "")
		return
	}
	if !model.textEditorActive() {
		view.Attr("editor-gutter", "hidden", "")
		// Keep the native textarea mounted while comparisons cover it so
		// returning to a file preserves its input and focus bindings.
		if model.sourceState().visible() {
			view.RemoveAttr("editor", "hidden")
		} else {
			view.Attr("editor", "hidden", "")
		}
		if model.activeFile() == nil && model.actions.ui.editor == nil {
			view.RemoveAttr("editor-empty", "hidden")
		} else {
			view.Attr("editor-empty", "hidden", "")
		}
	} else {
		view.RemoveAttr("editor-gutter", "hidden")
		view.RemoveAttr("editor", "hidden")
		view.Attr("editor-empty", "hidden", "")
	}

	for _, project := range model.projects {
		if project == nil {
			continue
		}
		view.AppendComponentChild("projects",
			componentKey(project.Path),
			projectDefinition,
			projectProps{Model: model, Theme: model.workbenchTheme, Icons: model.fileIcons, Project: project, ProblemRevision: model.bottom.problemRevision},
			struct{}{},
		)
	}

	if project := model.activeProjectState(); project != nil {
		for _, filePath := range project.Tabs {
			file := project.Files[filePath]
			if file == nil {
				continue
			}
			view.AppendComponentChild("file-tabs",
				componentKey(file.Path),
				tabDefinition,
				tabProps{Model: model, Theme: model.workbenchTheme, Icons: model.fileIcons, Project: project, Path: file.Path, Name: file.Name, Active: model.actions.ui.editor == nil && file.Path == project.ActivePath && !project.sourceControl.visible(), Preview: file.Preview, Dirty: file.Dirty, HasErrors: model.fileHasErrors(project, file.Path)},
				struct{}{},
			)
		}
	}
	model.renderExtensionTabs(view, tabDefinition)
	model.renderSourceWorkspace(view)
}

// Rendering only copies cached paint or submits an immutable snapshot. Parsing
// and line counting belong to the syntax worker, never the foreground frame.
func renderPencodeHighlight(model *workspaceModel) {
	if model == nil {
		return
	}
	if model.sourceState().visible() {
		if model.highlightedPath != "" || model.highlightRequest.file != nil {
			model.resetHighlight()
		}
		model.renderSourceHighlight()
		return
	}
	if model.highlightRequest.sourceDoc != nil {
		model.releaseSyntaxWorker()
	}
	file := model.activeFile()
	if file == nil || !model.textEditorActive() {
		if model.highlightedPath != "" || model.highlightRequest.file != nil {
			model.resetHighlight()
		}
		return
	}
	sourceChanged := model.highlightedPath != file.Path || model.highlightedContent != file.snapshot()
	if model.highlighted && !sourceChanged {
		return
	}
	if model.highlightedPath != file.Path {
		model.editor.brackets.source = piecetree.Snapshot{}
		model.editor.brackets.items = model.editor.brackets.items[:0]
		model.highlightPending = false
		model.highlightDue = time.Time{}
		model.highlightedPath, model.highlightedContent, model.highlighted = file.Path, piecetree.Snapshot{}, false
		model.highlightScratch = model.highlightScratch[:0]
		model.highlightPaintRevision++
		model.lineCount, model.characterCount, model.lineInfo = 0, 0, ""
	}
	model.ensureSyntaxTheme()
	if cached, ok := model.highlightCache.get(file, model.syntaxTheme, model.syntaxDefinitions); ok {
		model.highlightScratch = append(model.highlightScratch[:0], cached.spans...)
		model.editor.brackets.set(file.snapshot(), cached.brackets)
		model.highlightPaintRevision++
		model.highlightedPath, model.highlightedContent, model.highlighted = file.Path, file.snapshot(), true
		model.lineCount, model.characterCount = cached.lines, cached.characters
		format := model.tr("行 %d · %d 字符")
		if cached.lineInfoFormat != format {
			cached.lineInfo = fmt.Sprintf(format, cached.lines, cached.characters)
			cached.lineInfoFormat = format
		}
		model.lineInfo = cached.lineInfo
		return
	}
	if model.highlightPending {
		return
	}
	job := model.highlightRequest
	if job.file == file && job.path == file.Path && job.documentText == file.snapshot() && job.theme == model.syntaxTheme && job.definitions == model.syntaxDefinitions {
		return
	}
	model.submitHighlight(file)
}

func handlePencodeRootEvent(context *module.ContentEventContext[appProps, appState]) {
	if context == nil || context.Props == nil || context.Props.Model == nil {
		return
	}
	model := context.Props.Model
	inputContextDismissed := model.inputContext.open && !strings.HasPrefix(context.Ref, "input-context-")
	if inputContextDismissed {
		model.inputContext.open = false
		model.inputContext.activate = false
		model.inputContext.restore = false
		markPencodeRootDirty(context, model)
	}
	if overlay := model.actions.ui.overlay; overlay != nil && overlay.ui.Kind == peb.Dialog && model.closing.action == pencodeCloseNone && model.fileContext.dialog == "" && context.Ref != "window-close" {
		return
	}
	if model.connectionEvent(context.Ref, context.Event.Value) {
		markPencodeRootDirty(context, model)
		return
	}
	if model.handleSourceEvent(context.Event) {
		markPencodeRootDirty(context, model)
		return
	}
	if model.handleActionRef(context.World, context.Ref) {
		markPencodeRootDirty(context, model)
		return
	}
	model.lastFileClick = 0
	if model.handleTabContextEvent(context.World, context.Ref) {
		markPencodeRootDirty(context, model)
		return
	}
	if model.handleFolderSearchEvent(context.World, context.Ref, context.Event.Value) {
		markPencodeRootDirty(context, model)
		return
	}
	if model.handleFileContextEvent(context.World, context.Ref, context.Event.Value) {
		markPencodeRootDirty(context, model)
		return
	}
	if model.closing.action != pencodeCloseNone {
		model.handleCloseDialogEvent(context.World, context.Ref)
		markPencodeRootDirty(context, model)
		return
	}
	if model.handleBottomPanelEvent(context.World, context.Ref, context.Event.Value) {
		markPencodeRootDirty(context, model)
		return
	}
	if model.handleLanguageEvent(context) {
		markPencodeRootDirty(context, model)
		return
	}
	if model.handleQuickOpenEvent(context) {
		markPencodeRootDirty(context, model)
		return
	}
	if model.handleFindEvent(context) {
		markPencodeRootDirty(context, model)
		return
	}
	if model.handleSettingsEvent(context) {
		markPencodeRootDirty(context, model)
		return
	}
	if model.handleMarketplaceEvent(context) {
		markPencodeRootDirty(context, model)
		return
	}
	if context.Ref == "editor-page-position" {
		model.jumpFilePage(context.Event.Value)
		markPencodeRootDirty(context, model)
		return
	}
	if model.handleFilePage(context.Ref) {
		markPencodeRootDirty(context, model)
		return
	}
	switch context.Ref {
	case "editor-split":
		model.toggleEditorSplit()
		markPencodeRootDirty(context, model)
	case "editor-text", "editor-open-text":
		model.selectEditor(pencodeTextEditor)
		markPencodeRootDirty(context, model)
	case "window-close":
		if model.requestWindowClose() {
			handlePencodeWindowControl(model.app, context.Ref)
		}
		markPencodeRootDirty(context, model)
	case "window-minimize", "window-maximize":
		handlePencodeWindowControl(model.app, context.Ref)
	case "open-project", "change-project":
		model.chooseProject()
		if component, ok := context.World.Get[htmlui.Component[appProps, appState]](context.Component); ok {
			setPencodeEditorValue(context.World, component.Page, model.activeContent())
		}
		markPencodeRootDirty(context, model)
	case "collapse-directories":
		model.collapseDirectories()
		markPencodeRootDirty(context, model)
	case "reveal-active-file":
		model.revealActiveFile()
		markPencodeRootDirty(context, model)
	case "reload-from-disk":
		model.reloadFromDisk()
		if component, ok := context.World.Get[htmlui.Component[appProps, appState]](context.Component); ok {
			setPencodeEditorValue(context.World, component.Page, model.activeContent())
		}
		markPencodeRootDirty(context, model)
	case "editor":
		popup := model.languageUI.hover.open || model.languageUI.contextOpen || model.languages != nil && model.languages.panel.open
		previous := model.activeSnapshot()
		next := snapshotText(context.Event.Value)
		if context.Event.Document {
			next = context.Event.Snapshot
		}
		if model.applyFileSnapshot(model.activeFile(), model.activeProjectState(), next, true) {
			model.scheduleLanguageCompletion(context.World, context.Event, previous)
			model.cancelLanguageHover()
			model.dismissLanguageContext()
			if popup {
				markPencodeRootDirty(context, model)
			} else {
				model.refreshRegion(context.World, pencodeRefreshEditor)
			}
		}
	}
}

func handleProjectEvent(context *module.ContentEventContext[projectProps, struct{}]) {
	if context == nil || context.Props == nil || context.Props.Model == nil || context.Props.Project == nil {
		return
	}
	model := context.Props.Model
	if model.closing.action != pencodeCloseNone || model.fileDrag.suppressClick {
		return
	}
	model.lastFileClick = 0
	project := context.Props.Project
	switch context.Ref {
	case "toggle":
		model.toggleProject(project)
	case "close":
		model.closeProject(project)
	default:
		return
	}
	page := ecs.Entity(0)
	if component, ok := context.World.Get[htmlui.Component[projectProps, struct{}]](context.Component); ok {
		page = component.Page
	}
	setPencodeEditorValue(context.World, page, model.activeContent())
	markPencodeRootDirtyByModel(context.World, model)
}

func handleTreeEvent(context *module.ContentEventContext[treeProps, struct{}]) {
	if context == nil || context.Props == nil || context.Props.Model == nil || context.Props.Project == nil {
		return
	}
	props := context.Props
	model := props.Model
	if props.Editing {
		if context.Ref == "create-name" && context.Event.Kind == htmlui.EventInput && model.fileContext.inline() && model.fileContext.project == props.Project {
			model.fileContext.name, model.fileContext.err = context.Event.Value, ""
			markPencodeRootDirtyByModel(context.World, model)
		}
		return
	}
	if context.Ref != "row" {
		return
	}
	if model.closing.action != pencodeCloseNone || model.fileDrag.suppressClick {
		return
	}
	project := props.Project
	model.activateProject(project)
	if props.IsDir {
		model.lastFileClick = 0
		model.toggleProjectDirectory(project, props.Path, props.Depth)
		markPencodeRootDirtyByModel(context.World, model)
		return
	}
	if !model.canOpenFile(props.Path) {
		return
	}
	preview := !model.fileDoubleClick(context.Component, time.Now())
	if err := model.requestFileOpen(project, props.Path, preview, false, nil); err != nil {
		model.setProjectStatus(project, err.Error())
	}
	model.syncActiveProjectFields()
	// The clicked row is already visible; keep the tree under the pointer.
	model.revealProject, model.revealPath = nil, ""
	page := ecs.Entity(0)
	if component, ok := context.World.Get[htmlui.Component[treeProps, struct{}]](context.Component); ok {
		page = component.Page
	}
	setPencodeEditorValue(context.World, page, model.activeContent())
	model.finishSelectionRefresh(context.World)
}

func handleTabEvent(context *module.ContentEventContext[tabProps, struct{}]) {
	if context == nil || context.Props == nil || context.Props.Model == nil {
		return
	}
	props := context.Props
	if props.Surface != nil {
		if props.Model.closing.action != pencodeCloseNone {
			return
		}
		if context.Ref == "close" {
			props.Model.closeExtensionSurface(props.Surface, "button", true)
		} else if context.Ref == "select" {
			props.Model.selectExtensionTab(props.Surface)
		}
		markPencodeRootDirtyByModel(context.World, props.Model)
		return
	}
	if props.Project == nil {
		return
	}
	model := props.Model
	if model.closing.action != pencodeCloseNone {
		return
	}
	project := props.Project
	model.activateProject(project)
	if context.Ref == "close" {
		model.lastFileClick = 0
		model.closeProjectTab(project, props.Path)
	} else if context.Ref == "select" {
		if model.fileDoubleClick(context.Component, time.Now()) {
			pinProjectFile(project, props.Path)
		}
		model.selectProjectTab(project, props.Path)
	}
	page := ecs.Entity(0)
	if component, ok := context.World.Get[htmlui.Component[tabProps, struct{}]](context.Component); ok {
		page = component.Page
	}
	setPencodeEditorValue(context.World, page, model.activeContent())
	if context.Ref == "close" {
		markPencodeRootDirtyByModel(context.World, model)
	} else {
		model.finishSelectionRefresh(context.World)
	}
}

func markPencodeRootDirty(context *module.ContentEventContext[appProps, appState], model *workspaceModel) {
	if context == nil {
		return
	}
	if model != nil {
		model.fullRefresh = true
	}
	context.UpdateState(func(state *appState) { state.Revision++ })
	if model != nil && model.app != nil {
		model.app.RequestRender()
	}
}

func markPencodeRootDirtyByEntity(world *module.Content, entity ecs.Entity, model *workspaceModel) {
	if world == nil || entity == 0 {
		return
	}
	if model != nil {
		model.fullRefresh = true
	}
	_ = world.UpdateState[appProps, appState](entity, func(state *appState) {
		state.Revision++
	})
	if model != nil && model.app != nil {
		model.app.RequestRender()
	}
}

func markPencodeRootDirtyByModel(world *module.Content, model *workspaceModel) {
	if world == nil || model == nil {
		return
	}
	model.fullRefresh = true
	root := model.root
	if root != 0 {
		component, ok := world.Get[htmlui.Component[appProps, appState]](root)
		if !ok || component.Props.Model != model {
			root = 0
		}
	}
	if root == 0 {
		for entity, access := range world.Query[htmlui.Component[appProps, appState]]() {
			component := access.Get()

			if component.Props.Model == model {
				root = entity
				break
			}
		}
		model.root = root
	}
	if root == 0 {
		return
	}
	_ = world.UpdateState[appProps, appState](root, func(state *appState) {
		state.Revision++
	})
	if model.app != nil {
		model.app.RequestRender()
	}
}

func setPencodeEditorValue(world *module.Content, page ecs.Entity, value string) {
	if world == nil || page == 0 {
		return
	}
	if entity, ok := world.HTMLControl(page, "editor"); ok {
		if field := world.Mut[engineui.TextField](entity); field != nil {
			field.Buffer = nil
			field.Source = piecetree.Snapshot{}
		}
		world.SetControlValue(entity, value)
	}
}

func (model *workspaceModel) chooseProject() {
	if model != nil && model.session != nil && model.session.connection != nil && model.session.connection.workspaceProfile.Remote() {
		model.openRemoteProject(model.session.connection.workspaceProfile)
		return
	}
	if model == nil || model.app == nil || model.app.Context == nil {
		if model != nil {
			model.status = model.tr("文件对话框不可用")
		}
		return
	}
	directory, err := model.app.OpenFolder(window.FileDialogOptions{Title: model.tr("打开项目目录"), DefaultPath: model.lastDirectory})
	if err != nil {
		model.setActiveStatus(fmt.Sprintf(model.tr("打开项目失败：%v"), err))
		return
	}
	if directory == "" {
		return
	}
	if err := model.loadProject(directory); err != nil {
		model.setActiveStatus(err.Error())
		return
	}
}

func (model *workspaceModel) loadProject(directory string) error {
	if model == nil {
		return fmt.Errorf("%s", model.tr("项目工作区不可用"))
	}
	if model.backend == nil {
		return fmt.Errorf("%s", model.tr("工作区服务未连接"))
	}
	if model.session != nil && model.session.connection != nil && model.session.connection.restoring {
		return fmt.Errorf("%s", model.tr("连接恢复中，请稍后打开项目；未保存内容仍可继续编辑"))
	}
	if err := model.submitFileIO(&fileIOJob{kind: fileOpenWorkspace, root: directory}); err != nil {
		return err
	}
	model.status = model.tr("正在打开项目…")
	return nil
}

func (model *workspaceModel) acceptWorkspace(backend *client.Client, workspace protocol.Workspace) {
	model.rememberRecentProject(workspace)
	if index := model.projectIndex(workspace.Path); index >= 0 {
		model.activeProjectIndex = index
		model.screen = pencodeWorkspace
		model.resetHighlight()
		model.syncActiveProjectFields()
		return
	}
	project := &pencodeProject{
		backend: backend, workspace: workspace,
		Path: workspace.Path, Name: workspace.Name, Files: make(map[string]*openFile),
	}
	project.historyKey = workspace.Path
	if model.session != nil && model.session.connection != nil {
		project.historyKey = model.session.connection.namespace + workspace.Path
	}
	model.startProjectWatch(project)
	model.startProjectScan(project, true)
	model.projects = append(model.projects, project)
	model.activeProjectIndex = len(model.projects) - 1
	model.lastDirectory = workspace.Path
	model.screen = pencodeWorkspace
	model.resetHighlight()
	model.syncActiveProjectFields()
	model.restoreProjectState(project)
	return
}

func (model *workspaceModel) activeProjectState() *pencodeProject {
	if model == nil || model.activeProjectIndex < 0 || model.activeProjectIndex >= len(model.projects) {
		return nil
	}
	return model.projects[model.activeProjectIndex]
}

func (model *workspaceModel) projectIndex(directory string) int {
	for index, project := range model.projects {
		if project != nil && project.Path == directory {
			return index
		}
	}
	return -1
}

func (model *workspaceModel) syncActiveProjectFields() {
	if model == nil {
		return
	}
	previous := model.projectPath
	project := model.activeProjectState()
	if project == nil {
		model.revealProject, model.revealPath = nil, ""
		model.projectPath = ""
		model.projectName = ""
		model.entries = nil
		model.expandedDirs = nil
		model.files = nil
		model.tabs = nil
		model.activePath = ""
		model.allDirectoriesCollapsed = false
		model.syncExtensionProject(previous)
		return
	}
	if !project.tree.restore && (model.projectPath != project.Path || model.activePath != project.ActivePath) &&
		(model.revealProject != project || model.revealPath != project.ActivePath) {
		model.queueProjectFileReveal(project)
	}
	model.projectPath = project.Path
	model.projectName = project.Name
	model.entries = project.Entries
	model.expandedDirs = project.ExpandedDirs
	model.files = project.Files
	model.tabs = project.Tabs
	model.activePath = project.ActivePath
	model.allDirectoriesCollapsed = project.AllDirectoriesCollapsed
	model.status = project.Status
	model.syncExtensionProject(previous)
}

func (model *workspaceModel) setProjectStatus(project *pencodeProject, status string) {
	if model == nil {
		return
	}
	model.logOutput("pencode", status)
	if project != nil {
		project.Status = status
	}
	if project == model.activeProjectState() || model.activeProjectState() == nil {
		model.status = status
		model.bottom.lastStatus = status
	}
}

func (model *workspaceModel) resetHighlight() {
	if model == nil {
		return
	}
	model.releaseSyntaxWorker()
	model.scheduleSyntaxMemoryRelease()
	model.highlightScratch = model.highlightScratch[:0]
	model.highlightPaintRevision++
	model.highlightRebaseScratch = model.highlightRebaseScratch[:0]
	model.lineCount = 0
	model.characterCount = 0
	model.lineInfo = ""
	model.highlightedPath = ""
	model.highlightedContent = piecetree.Snapshot{}
	model.editor.brackets = pencodeBracketIndex{}
	model.editor.linkPaint, model.editor.compositionPaint = pencodeOverlayKey{}, pencodeOverlayKey{}
	model.editor.compositionSource, model.editor.compositionText, model.editor.compositionValue = piecetree.Snapshot{}, "", piecetree.Snapshot{}
	model.highlighted = false
	model.highlightPending = false
	model.highlightDue = time.Time{}
}

func (model *workspaceModel) scheduleSyntaxMemoryRelease() {
	model.syntaxMemoryDue = time.Now().Add(2 * time.Second)
	if model.app != nil && model.app.WakeAt != nil {
		model.app.WakeAt(model.syntaxMemoryDue)
	}
}

func (model *workspaceModel) releaseIdleSyntaxMemory(now time.Time) {
	if model.syntaxMemoryDue.IsZero() {
		return
	}
	if now.Before(model.syntaxMemoryDue) {
		// WakeAt keeps the earliest deadline; an older wake may arrive after
		// another edit has postponed reclamation.
		if model.app != nil && model.app.WakeAt != nil {
			model.app.WakeAt(model.syntaxMemoryDue)
		}
		return
	}
	model.syntaxMemoryDue = time.Time{}
	// Keep lightweight line states for the active document across pauses in
	// typing. Releasing them here would turn the next edit into a full scan.
	if model.sourceState().visible() || model.activeFile() != nil && model.textEditorActive() {
		return
	}
	model.releaseSyntaxWorker()
}

func (model *workspaceModel) activateProject(project *pencodeProject) {
	if model == nil || project == nil {
		return
	}
	index := model.projectIndex(project.Path)
	if index < 0 {
		return
	}
	if model.activeProjectIndex != index {
		model.fullRefresh = true
		model.cancelFileOpens()
		model.activeProjectIndex = index
		model.resetHighlight()
	}
	model.screen = pencodeWorkspace
	model.syncActiveProjectFields()
}

func (model *workspaceModel) toggleProject(project *pencodeProject) {
	if model == nil || project == nil {
		return
	}
	collapsed := !project.Collapsed
	model.activateProject(project)
	project.Collapsed = collapsed
}

func (model *workspaceModel) closeProject(project *pencodeProject) {
	if model == nil || project == nil || model.closing.action != pencodeCloseNone {
		return
	}
	if model.beginClose(pencodeCloseProject, project, "") {
		model.discardProject(project)
	}
}

func (model *workspaceModel) discardProject(project *pencodeProject) {
	if model == nil || project == nil {
		return
	}
	index := model.projectIndex(project.Path)
	if index < 0 {
		return
	}
	model.saveProjectState(project)
	model.notifyProjectClosed(project.Path)
	model.cancelProjectFileIO(project)
	if model.debug.project == project || model.debug.configProject == project {
		model.closeDebugger()
	}
	wasActive := index == model.activeProjectIndex
	project.stopScan()
	project.stopWatch()
	project.sourceControl.stop()
	model.closeTerminal(project.Path)
	for _, file := range project.Files {
		if file == nil {
			continue
		}
		model.highlightCache.remove(file)
		model.releaseFileFind(file)
		file.closeDocument()
		*file = openFile{}
	}
	clear(project.Tabs)
	project.Tabs = nil
	project.Files = nil
	project.PreviewPath = ""
	project.Entries = nil
	project.ExpandedDirs = nil
	model.releaseProjectWorkspace(project)
	copy(model.projects[index:], model.projects[index+1:])
	model.projects[len(model.projects)-1] = nil
	model.projects = model.projects[:len(model.projects)-1]
	if len(model.projects) == 0 {
		model.activeProjectIndex = -1
		model.screen = pencodeLauncher
		model.status = ""
		model.syncActiveProjectFields()
		model.resetHighlight()
		return
	}
	if wasActive {
		if index >= len(model.projects) {
			index = len(model.projects) - 1
		}
		model.activeProjectIndex = index
	} else if index < model.activeProjectIndex {
		model.activeProjectIndex--
	}
	model.screen = pencodeWorkspace
	model.resetHighlight()
	model.syncActiveProjectFields()
}

func (model *workspaceModel) isProjectDirectoryExpanded(project *pencodeProject, relative string, depth int) bool {
	if project == nil {
		return depth == 0
	}
	return project.ExpandedDirs[relative]
}

func (model *workspaceModel) isDirectoryExpanded(relative string, depth int) bool {
	if project := model.activeProjectState(); project != nil {
		return model.isProjectDirectoryExpanded(project, relative, depth)
	}
	if model == nil {
		return depth == 0
	}
	if model.allDirectoriesCollapsed {
		return model.expandedDirs[relative]
	}
	if expanded, ok := model.expandedDirs[relative]; ok {
		return expanded
	}
	return depth == 0
}

func (model *workspaceModel) toggleDirectory(relative string, depth int) {
	if project := model.activeProjectState(); project != nil {
		model.toggleProjectDirectory(project, relative, depth)
		return
	}
	if model == nil {
		return
	}
	if model.expandedDirs == nil {
		model.expandedDirs = make(map[string]bool)
	}
	model.expandedDirs[relative] = !model.isDirectoryExpanded(relative, depth)
}

func (model *workspaceModel) toggleProjectDirectory(project *pencodeProject, relative string, depth int) {
	if model == nil || project == nil {
		return
	}
	if project.ExpandedDirs == nil {
		project.ExpandedDirs = make(map[string]bool)
	}
	project.ExpandedDirs[relative] = !model.isProjectDirectoryExpanded(project, relative, depth)
	project.tree.revision++
	model.syncActiveProjectFields()
}

func (model *workspaceModel) collapseDirectories() {
	if project := model.activeProjectState(); project != nil {
		model.collapseProjectDirectories(project)
		return
	}
	if model == nil {
		return
	}
	model.allDirectoriesCollapsed = true
	clear(model.expandedDirs)
	model.status = model.tr("已折叠所有目录")
}

func (model *workspaceModel) collapseProjectDirectories(project *pencodeProject) {
	if model == nil || project == nil {
		return
	}
	project.AllDirectoriesCollapsed = true
	clear(project.ExpandedDirs)
	project.tree.revision++
	model.setProjectStatus(project, model.tr("已折叠所有目录"))
	model.syncActiveProjectFields()
}

func (model *workspaceModel) revealActiveFile() {
	if project := model.activeProjectState(); project != nil {
		model.revealProjectActiveFile(project)
		return
	}
	if model == nil {
		return
	}
	file := model.activeFile()
	if file == nil {
		model.status = model.tr("没有打开的文件")
		return
	}
	relative := path.Dir(file.Path)
	if relative != "." {
		if model.expandedDirs == nil {
			model.expandedDirs = make(map[string]bool)
		}
		for relative != "." && relative != "/" {
			model.expandedDirs[relative] = true
			next := path.Dir(relative)
			if next == relative {
				break
			}
			relative = next
		}
	}
	model.status = fmt.Sprintf(model.tr("已定位 %s"), file.Name)
}

func (model *workspaceModel) revealProjectActiveFile(project *pencodeProject) {
	if model == nil || project == nil {
		return
	}
	file := project.Files[project.ActivePath]
	if file == nil {
		model.setProjectStatus(project, model.tr("没有打开的文件"))
		return
	}
	model.queueProjectFileReveal(project)
	model.setProjectStatus(project, fmt.Sprintf(model.tr("已定位 %s"), file.Name))
	model.syncActiveProjectFields()
}

// Queue navigation without changing status or allocating row IDs before layout.
func (model *workspaceModel) queueProjectFileReveal(project *pencodeProject) {
	project.tree.restore = false
	file := project.Files[project.ActivePath]
	if file == nil {
		model.revealProject, model.revealPath = nil, ""
		return
	}
	project.Collapsed = false
	relative := path.Dir(file.Path)
	if relative != "." {
		if project.ExpandedDirs == nil {
			project.ExpandedDirs = make(map[string]bool)
		}
		for relative != "." && relative != "/" {
			if !project.ExpandedDirs[relative] {
				project.ExpandedDirs[relative] = true
				project.tree.revision++
			}
			next := path.Dir(relative)
			if next == relative {
				break
			}
			relative = next
		}
	}
	model.revealProject, model.revealPath = project, file.Path
}

// centerRevealedTreeRow centres the pending reveal row in its tree viewport.
// Progressive scans must finish before clamping; staged refreshes keep a stable
// visible snapshot that can still be navigated while the next one is built.
func (model *workspaceModel) centerRevealedTreeRow(world *module.Content) {
	if model == nil || model.revealProject == nil {
		return
	}
	project, relative := model.revealProject, model.revealPath
	if project != model.activeProjectState() || project.ActivePath != relative || project.Files[relative] == nil || project.Collapsed {
		model.revealProject, model.revealPath = nil, ""
		return
	}
	if model.sidebarHidden || project.scan != nil && !project.scan.staged {
		return
	}
	viewportID := treeRowID(project.Path)
	viewportNode, ok := world.HTMLNode(model.surface, viewportID)
	if !ok {
		return
	}
	_, ok = viewportNode.Bounds()
	if !ok {
		return
	}
	rowIndex := -1
	for i, index := range project.visibleTreeRows(model) {
		if index >= 0 && project.Entries[index].Path == relative {
			rowIndex = i
			break
		}
	}
	if rowIndex < 0 {
		if project.scan == nil {
			model.revealProject, model.revealPath = nil, ""
		}
		return
	}
	model.revealProject, model.revealPath = nil, ""
	area, ok := viewportNode.ScrollContainer()
	if !ok {
		return
	}
	project.tree.area = area
	scroll := world.Mut[sceneapi.UIScroll](area)
	if scroll == nil || !scroll.Vertical {
		return
	}
	// Every measurement here is a layout-space distance: HTML bounds exclude
	// scroll offsets, and UIScroll positions are layout-space content offsets.
	height := scroll.ViewportSize.Y()
	position := height/2 - float32(5+rowIndex*pencodeTreeRowHeight+pencodeTreeRowHeight/2)
	// Mirror the scroll system's own clamp: never positive, never past the end
	// of the content.
	overflow := max(scroll.ContentSize.Y()-height, 0)
	position = min(max(position, -overflow), 0)
	if position == scroll.Position.Y() {
		return
	}
	scroll.Position[1] = position
	if model.app != nil {
		model.app.RequestRender()
	}
}

// pencodeTreeScrollArea finds the scrolling tree viewport that owns a row.
func pencodeTreeScrollArea(world *module.Content, surface ecs.Entity, rowID string) (ecs.Entity, bool) {
	row, ok := world.HTMLControl(surface, rowID)
	if !ok {
		return 0, false
	}
	for entity := row; entity != 0; {
		parent, exists := world.Get[sceneapi.Parent](entity)
		if !exists || parent.Entity == 0 {
			break
		}
		entity = parent.Entity
		if _, scrolled := world.Get[sceneapi.UIScroll](entity); scrolled {
			return entity, true
		}
	}
	return 0, false
}

func (model *workspaceModel) reloadFromDisk() {
	if project := model.activeProjectState(); project != nil {
		model.reloadProjectFromDisk(project)
		return
	}
	if model != nil {
		model.status = model.tr("没有打开的项目")
	}
}

func (model *workspaceModel) reloadProjectFromDisk(project *pencodeProject) {
	if model == nil || project == nil || project.Path == "" {
		if model != nil {
			model.status = model.tr("没有打开的项目")
		}
		return
	}
	if err := model.requestFileReload(project, false); err != nil {
		model.setProjectStatus(project, err.Error())
		return
	}
	model.startProjectScan(project, false)
}

func skipProjectEntry(name string) bool {
	switch name {
	case ".DS_Store", ".git", "dist", "node_modules":
		return true
	default:
		return false
	}
}

func isTextFile(name string) bool { return protocol.IsTextFile(name) }

func selectProjectFile(project *pencodeProject, relative string, preview bool) {
	project.sourceControl.Active = ""
	project.sourceControl.Selection++
	if !preview {
		pinProjectFile(project, relative)
	}
	project.ActivePath = relative
	project.Status = ""
}

func acceptProjectFile(project *pencodeProject, relative string, preview bool, document *client.Document, content string) {
	if project.Files == nil {
		project.Files = make(map[string]*openFile)
	}
	project.sourceControl.Active = ""
	project.sourceControl.Selection++
	file := &openFile{document: document, Path: relative, Name: path.Base(relative), contentSnapshot: snapshotText(content), unsupported: document == nil, Preview: preview}
	file.savedContent = file.snapshot()
	file.initPage()
	project.Files[relative] = file
	replaced := false
	if preview && project.PreviewPath != "" {
		previous := project.Files[project.PreviewPath]
		if previous != nil && previous.Preview && !previous.Dirty {
			for index, tab := range project.Tabs {
				if tab == project.PreviewPath {
					project.Tabs[index] = relative
					delete(project.Files, project.PreviewPath)
					previous.closeDocument()
					*previous = openFile{}
					replaced = true
					break
				}
			}
		} else {
			pinProjectFile(project, project.PreviewPath)
		}
	}
	if !replaced {
		project.Tabs = append(project.Tabs, relative)
	}
	if preview {
		project.PreviewPath = relative
	}
	project.ActivePath = relative
	project.Status = ""
}

func (model *workspaceModel) openFile(relative string) {
	if model == nil || !model.canOpenFile(relative) {
		return
	}
	if project := model.activeProjectState(); project != nil {
		model.openProjectFile(project, relative)
		return
	}
	if err := model.requestFileOpen(nil, relative, false, true, nil); err != nil {
		model.setActiveStatus(err.Error())
	}
}

func (model *workspaceModel) openProjectFile(project *pencodeProject, relative string) {
	if model == nil || project == nil || !model.canOpenFile(relative) {
		return
	}
	model.activateProject(project)
	if err := model.requestFileOpen(project, relative, false, true, nil); err != nil {
		model.setProjectStatus(project, err.Error())
	}
	model.syncActiveProjectFields()
}

func (model *workspaceModel) selectProjectTab(project *pencodeProject, relative string) {
	if model == nil || project == nil || project.Files[relative] == nil {
		return
	}
	before := model.selectionSnapshot()
	defer func() { model.refreshFileSelection(before) }()
	model.actions.ui.editor = nil
	model.cancelFileOpens()
	model.activateProject(project)
	if project.sourceControl.visible() {
		model.editor.file = nil
	}
	project.sourceControl.Active = ""
	project.sourceControl.Selection++
	project.ActivePath = relative
	project.Status = ""
	model.queueProjectFileReveal(project)
	model.syncActiveProjectFields()
}

func (model *workspaceModel) selectTab(relative string) {
	if model == nil {
		return
	}
	if project := model.activeProjectState(); project != nil {
		model.selectProjectTab(project, relative)
		return
	}
	if model.files[relative] == nil {
		return
	}
	model.actions.ui.editor = nil
	model.cancelFileOpens()
	model.activePath = relative
	model.status = ""
}

func (model *workspaceModel) closeProjectTab(project *pencodeProject, relative string) {
	if model == nil || project == nil || model.closing.action != pencodeCloseNone {
		return
	}
	if !model.beginClose(pencodeCloseTab, project, relative) {
		return
	}
	model.discardProjectTab(project, relative)
}

func (model *workspaceModel) discardProjectTab(project *pencodeProject, relative string) {
	model.cancelFileOpen(project, relative)
	for index, tab := range project.Tabs {
		if tab != relative {
			continue
		}
		wasActive := project == model.activeProjectState() && model.actions.ui.editor == nil && !project.sourceControl.visible() && project.ActivePath == relative
		copy(project.Tabs[index:], project.Tabs[index+1:])
		project.Tabs[len(project.Tabs)-1] = ""
		project.Tabs = project.Tabs[:len(project.Tabs)-1]
		if project.ActivePath == relative {
			if index < len(project.Tabs) {
				project.ActivePath = project.Tabs[index]
			} else if len(project.Tabs) > 0 {
				project.ActivePath = project.Tabs[len(project.Tabs)-1]
			} else {
				project.ActivePath = ""
			}
		}
		if file := project.Files[relative]; file != nil {
			model.highlightCache.remove(file)
			model.releaseFileFind(file)
			file.closeDocument()
			*file = openFile{}
		}
		if project.PreviewPath == relative {
			project.PreviewPath = ""
		}
		delete(project.Files, relative)
		if model.highlightedPath == relative {
			model.resetHighlight()
		}
		model.syncActiveProjectFields()
		// Extension tabs follow file tabs in the strip.
		if wasActive && index == len(project.Tabs) {
			model.selectFirstExtensionTab()
		}
		return
	}
}

func (model *workspaceModel) closeTab(relative string) {
	if model == nil || model.closing.action != pencodeCloseNone {
		return
	}
	if project := model.activeProjectState(); project != nil {
		model.closeProjectTab(project, relative)
		return
	}
	if !model.beginClose(pencodeCloseTab, nil, relative) {
		return
	}
	model.discardTab(relative)
}

func (model *workspaceModel) discardTab(relative string) {
	for index, tab := range model.tabs {
		if tab != relative {
			continue
		}
		wasActive := model.actions.ui.editor == nil && !model.sourceState().visible() && model.activePath == relative
		copy(model.tabs[index:], model.tabs[index+1:])
		model.tabs[len(model.tabs)-1] = ""
		model.tabs = model.tabs[:len(model.tabs)-1]
		if model.activePath == relative {
			if index < len(model.tabs) {
				model.activePath = model.tabs[index]
			} else if len(model.tabs) > 0 {
				model.activePath = model.tabs[len(model.tabs)-1]
			} else {
				model.activePath = ""
			}
		}
		if file := model.files[relative]; file != nil {
			model.highlightCache.remove(file)
			model.releaseFileFind(file)
			file.closeDocument()
			*file = openFile{}
		}
		delete(model.files, relative)
		if model.highlightedPath == relative {
			model.resetHighlight()
		}
		if wasActive && index == len(model.tabs) {
			model.selectFirstExtensionTab()
		}
		return
	}
}

func (model *workspaceModel) updateActiveContent(value string) bool {
	return model.applyActiveContent(value, true)
}

func (model *workspaceModel) applyActiveContent(value string, recordHistory bool) bool {
	return model.applyFileContent(model.activeFile(), model.activeProjectState(), value, recordHistory)
}

func (model *workspaceModel) applyFileContent(file *openFile, project *pencodeProject, value string, recordHistory bool) bool {
	return model.applyFileSnapshot(file, project, snapshotText(value), recordHistory)
}
func (model *workspaceModel) applyFileSnapshot(file *openFile, project *pencodeProject, value piecetree.Snapshot, recordHistory bool) bool {
	if file == nil || !file.text() || file.pageLoading || file.snapshot().Equal(value) {
		return false
	}
	start, oldSuffix, newSuffix, prefixRunes, changed := file.buffer.SnapshotChangedRange(file.snapshot(), value)
	if !changed {
		start, oldSuffix, newSuffix = file.snapshot().ChangedRange(value)
		prefixRunes = value.RuneOffset(start)
	}
	if !file.buffer.MatchesSnapshot(value) {
		file.snapshotBuffer()
		file.buffer.ReplaceBytes(start, oldSuffix, value.Slice(start, newSuffix))
		value = file.buffer.Snapshot()
	}
	if recordHistory {
		if !file.Dirty && file.savedContent.Len() == 0 {
			file.savedContent = file.snapshot()
		}
		file.recordEdit(pencodeEdit{
			start: start, removed: file.snapshot().Slice(start, oldSuffix), inserted: value.Slice(start, newSuffix),
			oldCaret: prefixRunes + utf8.RuneCountInString(file.snapshot().Slice(start, oldSuffix)),
			newCaret: prefixRunes + utf8.RuneCountInString(value.Slice(start, newSuffix)),
		})
	}
	if file == model.activeFile() && model.lineCount > 0 && model.highlightedPath == file.Path && model.highlightedContent == file.snapshot() {
		model.rebaseHighlight(file.snapshot(), value, start, oldSuffix, newSuffix)
	}
	model.highlightCache.invalidate(file)
	if file.document != nil {
		file.document.Update(file.snapshot(), value, start, oldSuffix, newSuffix)
	}
	if file.page.Paged {
		file.page.Size = file.page.Size - uint64(file.snapshot().Len()) + uint64(value.Len())
	}
	file.contentSnapshot = value
	file.updatePageLabel()
	model.rebaseDebugBreakpoints(project, file, value, start, oldSuffix, newSuffix)
	model.rebaseFindScope(file, start, oldSuffix, newSuffix)
	file.Dirty = file.pageDirty || !file.snapshot().Equal(file.savedContent)
	if project != nil {
		pinProjectFile(project, file.Path)
	}
	if file == model.activeFile() {
		model.highlightPending = true
		model.highlightDue = time.Now().Add(pencodeHighlightDebounce)
		if model.app != nil && model.app.WakeAt != nil {
			model.app.WakeAt(model.highlightDue)
		}
	}
	if project != nil {
		model.setProjectStatus(project, "")
	} else {
		model.status = ""
	}
	return true
}

func (model *workspaceModel) rebaseHighlight(oldContent, content piecetree.Snapshot, prefix, oldSuffix, newSuffix int) {
	for _, character := range oldContent.Slice(prefix, oldSuffix) {
		model.characterCount--
		if character == '\n' {
			model.lineCount--
		}
	}
	for _, character := range content.Slice(prefix, newSuffix) {
		model.characterCount++
		if character == '\n' {
			model.lineCount++
		}
	}
	model.lineInfo = fmt.Sprintf(model.tr("行 %d · %d 字符"), model.lineCount, model.characterCount)

	spans := rebasePencodeSpans(model.highlightRebaseScratch[:0], model.highlightScratch, prefix, oldSuffix, newSuffix)
	model.highlightRebaseScratch, model.highlightScratch = model.highlightScratch[:0], spans
	model.highlightPaintRevision++
	model.highlightedContent = content
}

// Keep the edit's color until the debounced parser supplies its final tokens.
// Insertion at a token boundary continues the token on the left.
func rebasePencodeSpans(dst, source []sceneapi.TextSpan2D, prefix, oldSuffix, newSuffix int) []sceneapi.TextSpan2D {
	shift := newSuffix - oldSuffix
	inserted := false
	for _, span := range source {
		if span.Start < prefix {
			dst = appendPencodeSpan(dst, sceneapi.TextSpan2D{Start: span.Start, End: min(span.End, prefix), Color: span.Color})
		}
		if !inserted && newSuffix > prefix && span.Start <= prefix && prefix <= span.End {
			dst = appendPencodeSpan(dst, sceneapi.TextSpan2D{Start: prefix, End: newSuffix, Color: span.Color})
			inserted = true
		}
		if span.End > oldSuffix {
			dst = appendPencodeSpan(dst, sceneapi.TextSpan2D{Start: max(span.Start, oldSuffix) + shift, End: span.End + shift, Color: span.Color})
		}
	}
	return dst
}

func appendPencodeSpan(spans []sceneapi.TextSpan2D, span sceneapi.TextSpan2D) []sceneapi.TextSpan2D {
	if len(spans) > 0 && spans[len(spans)-1].End == span.Start && spans[len(spans)-1].Color == span.Color {
		spans[len(spans)-1].End = span.End
		return spans
	}
	return append(spans, span)
}

func updatePencodeHighlight(context *module.ContentSystemContext, model *workspaceModel) error {
	if context == nil || context.World == nil || model == nil {
		return nil
	}
	changed := model.pollHighlight()
	if model.highlightPending && !time.Now().Before(model.highlightDue) {
		model.highlightPending = false
		model.highlightDue = time.Time{}
		model.highlighted = false
		changed = true
	}
	if changed {
		if model.sourceState().visible() {
			markPencodeRootDirtyByModel(context.World, model)
		} else {
			model.refreshRegion(context.World, pencodeRefreshEditor)
		}
	}
	return nil
}

// Titlebar popups keep their toggle button in the inside region so a second
// click on the toggle closes them through their own click handler.
var (
	pencodeQuickOpenRefs   = [...]string{"file-search", "quick-open-panel"}
	pencodeMarketplaceRefs = [...]string{"marketplace-button", "marketplace-panel"}
	pencodeConnectionRefs  = [...]string{"connection-open", "connection-panel", "connection-menu"}
	pencodeSettingsRefs    = [...]string{"settings-button", "settings-panel"}
	pencodeSettingsSelects = [...]string{"theme-select", "icon-theme-select", "code-font-select", "ui-font-select"}
)

func (model *workspaceModel) pointerInsideRefs(world *module.Content, x, y float32, refs []string) bool {
	for _, ref := range refs {
		node, ok := world.HTMLNode(model.surface, ref)
		if !ok {
			continue
		}
		bounds, ok := node.Bounds()
		if ok && x >= bounds.X && x < bounds.X+bounds.Width && y >= bounds.Y && y < bounds.Y+bounds.Height {
			return true
		}
	}
	return false
}

// dismissPopupsOutside closes the quick open, marketplace or settings popup once
// a press lands outside its panel.
func (model *workspaceModel) dismissPopupsOutside(world *module.Content, state *input.State) bool {
	connectionOpen := model.session != nil && model.session.connection != nil && model.session.connection.open
	if connectionOpen && model.session.connection.picker.open {
		return false
	}
	if !model.quickOpen && !model.settingsOpen && !connectionOpen && (model.market == nil || !model.market.open) {
		return false
	}
	if !state.PointerPressed() && !state.MousePressed(input.MouseRight) && !state.MousePressed(input.MouseMiddle) {
		return false
	}
	// While recording a shortcut, the next press belongs to the settings panel.
	if model.capturingShortcut {
		return false
	}
	zoom := float32(model.settings.Zoom) / 100
	pointer := state.Pointer()
	x, y := pointer.X/zoom, pointer.Y/zoom
	switch {
	case model.quickOpen:
		if model.pointerInsideRefs(world, x, y, pencodeQuickOpenRefs[:]) {
			return false
		}
		model.closeQuickOpen(world)
		return true
	case model.market != nil && model.market.open:
		if model.pointerInsideRefs(world, x, y, pencodeMarketplaceRefs[:]) {
			return false
		}
		// The press owns focus, so unlike the close button this keeps it off the toggle.
		model.market.open = false
		return true
	case connectionOpen:
		if model.session.connection.passwordRequest != nil {
			return false
		}
		if model.pointerInsideRefs(world, x, y, pencodeConnectionRefs[:]) {
			return false
		}
		model.connectionEvent("connection-close", "")
		return true
	case model.settingsSelectOpen(world) || model.pointerInsideRefs(world, x, y, pencodeSettingsRefs[:]):
		// An option picker escapes below the panel, so its rows are not outside it.
		return false
	default:
		model.settingsOpen = false
		return true
	}
}

func (model *workspaceModel) settingsSelectOpen(world *module.Content) bool {
	for _, ref := range pencodeSettingsSelects {
		entity, ok := world.HTMLControl(model.surface, ref)
		if !ok {
			continue
		}
		if control, ok := world.Get[htmlui.Control](entity); ok && control.Open {
			return true
		}
	}
	return false
}

func updatePencodeShortcuts(context *module.ContentSystemContext, model *workspaceModel) error {
	if context == nil || context.World == nil || model == nil {
		return nil
	}
	model.syncZoom(context.World)
	state, ok := context.World.Resource[*input.State]()
	if !ok {
		return nil
	}
	if handled, changed := model.updateInputContext(context.World, state); handled {
		if changed {
			markPencodeRootDirtyByModel(context.World, model)
		}
		return nil
	} else if changed {
		markPencodeRootDirtyByModel(context.World, model)
	}
	if model.updateExtensionSelection(context.World, state) {
		return nil
	}
	handled, uiChanged := model.updateExtensionUI(context.World, state)
	if uiChanged {
		markPencodeRootDirtyByModel(context.World, model)
	}
	if handled {
		return nil
	}
	if handled, changed := model.updateDebugTemplateMenu(context.World, state); handled {
		if changed {
			markPencodeRootDirtyByModel(context.World, model)
		}
		return nil
	} else if changed {
		markPencodeRootDirtyByModel(context.World, model)
	}
	model.updateTabWheel(context.World, state)
	model.updateFilePageWheel(context.World, state)
	changed := model.updateWorkbenchResize(context.World, state)
	if model.updateDebugPointer(context.World, state) {
		changed = true
	}
	if model.updateTerminalPointer(context.World, state) {
		changed = true
	}
	if model.updateSourceControl(context.World, state) {
		changed = true
	}
	if model.dismissPopupsOutside(context.World, state) {
		changed = true
	}
	if model.updateFileDrag(context.World, state) {
		changed = true
	}
	if model.updateFileContext(context.World, state) {
		changed = true
	}
	if model.updateTabContext(context.World, state) {
		changed = true
	}
	if model.updateLanguageInteractions(context.World, state) {
		changed = true
	}
	if model.handleShortcutKeys(context.World, state, runtime.GOOS) {
		changed = true
	}
	if changed {
		markPencodeRootDirtyByModel(context.World, model)
	}
	return nil
}

func pencodeSaveShortcut(state *input.State, goos string) bool {
	return pencodeShortcutDefinitions[pencodeShortcutSave].defaultKey.matches(state, goos)
}

func (model *workspaceModel) saveActiveFile() {
	if model == nil {
		return
	}
	file := model.activeFile()
	if file == nil {
		model.setActiveStatus(model.tr("没有打开的文件"))
		return
	}
	if document := model.customDocuments[file]; document != nil && document.busy() {
		document.savePending = true
		return
	}
	if _, err := model.requestFileSave(model.activeProjectState(), file); err != nil {
		model.setActiveStatus(err.Error())
	}
}

func (model *workspaceModel) setActiveStatus(status string) {
	if model == nil {
		return
	}
	if project := model.activeProjectState(); project != nil {
		model.setProjectStatus(project, status)
		model.syncActiveProjectFields()
		return
	}
	model.status = status
}

func (model *workspaceModel) activeFile() *openFile {
	if model == nil || model.actions.ui.editor != nil {
		return nil
	}
	if project := model.activeProjectState(); project != nil {
		if project.ActivePath == "" {
			return nil
		}
		return project.Files[project.ActivePath]
	}
	if model.activePath == "" {
		return nil
	}
	return model.files[model.activePath]
}

func (model *workspaceModel) activeContent() string {
	if file := model.activeFile(); file != nil {
		return file.content()
	}
	return ""
}

func treeRowID(relative string) string {
	const hex = "0123456789abcdef"
	var builder strings.Builder
	builder.Grow(len(relative) + len("tree-row-"))
	builder.WriteString("tree-row-")
	for index := 0; index < len(relative); index++ {
		value := relative[index]
		if value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' {
			builder.WriteByte(value)
			continue
		}
		builder.WriteByte('_')
		builder.WriteByte(hex[value>>4])
		builder.WriteByte(hex[value&0xf])
	}
	return builder.String()
}

func (model *workspaceModel) projectTreeRowID(project *pencodeProject, relative string) string {
	if model == nil || project == nil || len(model.projects) <= 1 {
		return treeRowID(relative)
	}
	return treeRowID(project.Path + "\x00" + relative)
}

func treeRowClass(props *treeProps) string {
	if props == nil {
		return "pencode-tree-row"
	}
	if props.Model != nil && props.Model.fileDrag.active {
		drag := &props.Model.fileDrag
		if drag.source == props.Project && drag.path == props.Path {
			return "pencode-tree-row drag-source"
		}
		if props.IsDir && drag.isTarget(props.Project, props.Path) {
			return "pencode-tree-row directory drop-target"
		}
	}
	if props.Model != nil && props.Model.fileContext.open && props.Model.fileContext.project == props.Project && props.Model.fileContext.path == props.Path {
		if props.IsDir {
			return "pencode-tree-row directory context-selected"
		}
		return "pencode-tree-row context-selected"
	}
	if props.IsDir {
		return "pencode-tree-row directory"
	}
	if treeRowActive(props) {
		return "pencode-tree-row active"
	}
	return "pencode-tree-row"
}

func treeRowActive(props *treeProps) bool {
	if props == nil || props.Model == nil || props.IsDir {
		return false
	}
	if props.Project == nil {
		return props.Path == props.Model.activePath
	}
	return props.Project == props.Model.activeProjectState() && props.Path == props.Project.ActivePath
}

func treeIconForeground(props *treeProps) string {
	if treeRowActive(props) {
		return "list.activeSelectionForeground"
	}
	return "icon.foreground"
}

func treeIcon(props *treeProps) string {
	if props != nil && props.Model != nil {
		if source := props.Model.fileIcons.source(props.Name, props.IsDir); source != "" {
			return source
		}
	}
	if props != nil && props.IsDir {
		if props.Model != nil && props.Model.isProjectDirectoryExpanded(props.Project, props.Path, props.Depth) {
			return "icons/folder-opened.svg"
		}
		return "icons/folder.svg"
	}
	return "icons/file-text.svg"
}

func boolAttribute(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func componentKey(value string) uint64 {
	key := uint64(14695981039346656037)
	for index := 0; index < len(value); index++ {
		key ^= uint64(value[index])
		key *= 1099511628211
	}
	return key
}

func defaultDirectory() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home
	}
	return "."
}

func configurePencodeWindow(app *module.Host, model *workspaceModel) {
	if app == nil {
		return
	}

	if app.Context == nil {
		return
	}
	app.Context.SetDecorations(window.DecorationNone)
	model.windowTitlebarHeight = pencodeNativeTitlebarHeight(float64(model.settings.Zoom) / 100)
	app.Context.SetTitleBarHeight(model.windowTitlebarHeight)
	app.Context.SetBorderVisible(false)
	app.Context.SetResizable(true)
	app.SetWindowDragRegion(func(x, y float64) bool {
		if model.closing.action != pencodeCloseNone {
			return false
		}
		width := pencodeViewportWidth
		if viewportWidth, _ := app.GetViewport(); viewportWidth > 0 {
			width = float64(viewportWidth)
		}
		zoom := float64(model.settings.Zoom) / 100
		if model.pointerInsideRefs(model.fontResources, float32(x/zoom), float32(y/zoom), []string{"actions-titlebar", "connection-open"}) {
			return false
		}
		return pencodeWindowDragRegion(width, zoom, x, y)
	})
}

func pencodeWindowDragRegion(width, zoom, x, y float64) bool {
	if y < 0 || y >= float64(pencodeTitlebarHeight+pencodeTitlebarInset)*zoom {
		return false
	}
	searchInset := float64(456) // HTML borders/insets (16px) plus search max-width inset.
	searchHalfWidth := max(0, min(360*zoom, width-searchInset*zoom)) / 2
	if x >= width/2-searchHalfWidth && x < width/2+searchHalfWidth {
		return false
	}
	if runtime.GOOS == "darwin" {
		// AppKit buttons occupy x=20..82 in window points, independent of UI zoom.
		return x >= 98 && x < width-80*zoom
	}
	// Reserve the titlebar's right inset and border (8px), marketplace inset (182px)
	// and button width (28px), so window dragging cannot consume button clicks.
	return x >= 54*zoom && x < width-218*zoom
}

func pencodeNativeTitlebarHeight(zoom float64) float64 {
	// AppKit centers its controls in this height, measured from the window top.
	return float64(pencodeTitlebarHeight+2*pencodeTitlebarInset) * zoom
}

func handlePencodeWindowControl(app *module.Host, id string) {
	if app == nil || app.Context == nil {
		return
	}
	switch id {
	case "window-minimize":
		app.Minimize()
	case "window-maximize":
		app.ToggleMaximize()
	case "window-close":
		app.RequestClose()
	}
}

type pencodeIconProvider struct {
	content        *module.Content
	model          *workspaceModel
	textures       map[string]*pencodeCachedImage
	images         map[*graphics.Texture]*pencodeCachedImage
	oldest, newest *pencodeCachedImage
	theme          *pencodeTheme
	root           string
	bytes          int
	touched        bool
	imageRevision  uint64
}

func (provider *pencodeIconProvider) ResolveHTMLImage(source string, width, height float32) (*graphics.Texture, bool) {
	if provider == nil || source == "" {
		return nil, false
	}
	if entry, ok := provider.textures[source]; ok {
		texture := entry.texture
		if texture == nil {
			return nil, false
		}
		current, _ := texture.RasterScale()
		if provider.iconScale(texture.LogicalWidth(), texture.LogicalHeight(), width, height) <= int(current) || !entry.icon {
			provider.touch(entry)
			return texture, true
		}
	}
	asset, foreground, themed := strings.Cut(source, "?foreground=")
	if themed {
		if len(foreground) != 6 && len(foreground) != 8 {
			return nil, false
		}
		if _, err := hex.DecodeString(foreground); err != nil {
			return nil, false
		}
	} else {
		foreground = "cbd5e1"
	}
	var data []byte
	var err error
	preview := false
	if strings.HasPrefix(asset, "peb://") {
		if provider.model != nil {
			data = provider.model.extensionImages[asset].data
			preview = data != nil
			if !preview {
				data = provider.model.extensionIcons[asset]
			}
		}
		if data == nil {
			// A workspace can render shared actions before Enter transfers the
			// extension assets from the launcher. Retry once they are available.
			return nil, false
		}
	} else {
		file := filepath.FromSlash(asset)
		absolute := filepath.IsAbs(file)
		if !absolute {
			file = filepath.Join(provider.root, file)
		}
		data, err = os.ReadFile(file)
		if !absolute && provider.content != nil {
			data, err = provider.content.ReadFile("assets/" + filepath.ToSlash(asset))
		}
		if err != nil && !absolute {
			if sourceFS, projectRoot, ok := resource.Default(); ok {
				data, err = fs.ReadFile(sourceFS, path.Join(projectRoot, "resources", filepath.ToSlash(asset)))
			}
		}
	}
	var texture *graphics.Texture
	if err == nil {
		extension := strings.ToLower(filepath.Ext(asset))
		if extension == ".svg" {
			data = []byte(strings.ReplaceAll(string(data), "currentColor", "#"+foreground))
		}
		if extension == ".jpeg" {
			extension = ".jpg"
		}
		name := "asset" + extension
		decoder := graphics.ImageDecoderSet[
			ontex.Decoder,
			graphicssvg.Decoder,
			png.Decoder,
			jpeg.Decoder,
			webp.Decoder,
		]{}
		options := graphics.TextureOptions{Usage: graphics.ResourceUsageGPU}
		icon := extension == ".svg" && !preview
		if icon {
			config, configErr := onixsvg.DecodeConfig(bytes.NewReader(data))
			if configErr != nil {
				err = configErr
			} else {
				options.Mipmap = true
				texture, err = graphicssvg.DecodeAtScale(data, provider.iconScale(config.Width, config.Height, width, height), options)
			}
		} else if preview && extension == ".svg" {
			// Editor images retain the worker-validated pixel budget. The icon
			// supersampling factor would multiply their texture memory by 16.
			texture, err = graphicssvg.DecodeAtScale(data, 1, options)
		} else {
			texture, err = decoder.DecodeImage(data, name, options)
		}
	}
	if err != nil {
		if entry := provider.textures[source]; entry != nil && entry.texture != nil {
			provider.touch(entry)
			return entry.texture, true
		}
		texture = nil
	}
	if provider.textures == nil {
		provider.textures = make(map[string]*pencodeCachedImage)
	}
	provider.cacheImage(source, texture, strings.EqualFold(filepath.Ext(asset), ".svg") && !preview, preview)
	return texture, texture != nil
}
