package protocol

// DebugAdapterCommand resolves a project launcher on the workspace host.
// Override is the host-level adapter path from Settings. Command takes priority.
type DebugAdapterCommand struct {
	Adapter, Command, Address, Override string
	Args                                []string
}

type TerminalCommand struct {
	Env       map[string]*string
	Directory string
	Args      []string
}
