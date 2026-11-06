package protocol

// Service operations share the connection's cancellation and chunking rules.
type Operation uint32

const (
	GitCommand Operation = iota + 1
	FileRead
	FileSave
	DiffPatch
	ProcessOpen
	TerminalOpen
	StreamRead
	StreamWrite
	StreamResize
	StreamClose
	StreamWait
	IndexOpen
	IndexNext
	IndexClose
	PathSearch
	SyntaxAnalyze
	SyntaxRelease
	ExtensionList
	ExtensionLoad
	ExtensionResource
	ExtensionProcess
	ExtensionLanguageProcess
	ExtensionBegin
	ExtensionWrite
	ExtensionPrepare
	ExtensionCommit
	ExtensionAbort
	ExtensionEnable
	ExtensionRemove
	ExtensionResources
	DirectoryBrowse
	ConfigurationRead
	SettingsSave
	PortList
	PortConnect
	StreamCloseWrite
	GitRepositories
	TextSearch
	DebugAdapterOpen
	DebugTerminalOpen
	WorkspaceWatch
)

type WorkspaceChange uint32

const (
	WorkspaceTreeChanged WorkspaceChange = 1 << iota
	WorkspaceGitChanged
	WorkspaceRepositoriesChanged
)

type Port struct {
	Host    string
	Process string
	PID     uint32
	Number  uint16
}

type IndexEntry struct {
	Path, Name string
	Depth      int
	Directory  bool
}

// FileOperation is shared by every native watcher and file-service consumer.
type FileOperation uint8

const (
	FileCreated FileOperation = 1 << iota
	FileWritten
	FileRemoved
	FileRenamed
	FileMetadataChanged
)

type FileEvent struct {
	Path      string // Workspace-relative, slash-separated; never an external Git path.
	Op        FileOperation
	Directory bool
}
type FileChanges struct {
	Files   []FileEvent
	Version uint64
	Changes WorkspaceChange
	Reset   bool // Reconcile when history or native events overflowed.
}
