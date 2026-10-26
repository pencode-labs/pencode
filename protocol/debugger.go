package protocol

type TerminalCommand struct {
	Directory string
	Args      []string
	Env       map[string]*string
}
