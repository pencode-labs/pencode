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
)

type Port struct {
	Number  uint16
	Host    string
	PID     uint32
	Process string
}

type IndexEntry struct {
	Path, Name string
	Depth      int
	Directory  bool
}

type PathRule struct {
	Pattern string
	Suffix  bool
}
