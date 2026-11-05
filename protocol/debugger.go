package protocol

type TerminalCommand struct {
	Env       map[string]*string
	Directory string
	Args      []string
}
