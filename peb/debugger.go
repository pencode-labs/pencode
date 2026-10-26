package peb

// DebugAdapter supplies an executable and argument prefix for an adapter ID
// (for example Delve). The workspace server owns its process and transport.
// Command follows the same path rules as LanguageServer.Command.
type DebugAdapter struct {
	ID, Label, Command string
	Args               []string
}

func encodeDebugAdapters(e *encoder, adapters []DebugAdapter) {
	e.count(len(adapters))
	for _, a := range adapters {
		e.str(a.ID)
		e.str(a.Label)
		e.str(a.Command)
		e.count(len(a.Args))
		for _, arg := range a.Args {
			e.str(arg)
		}
	}
}

func decodeDebugAdapters(d *decoder) []DebugAdapter {
	n := d.count()
	if n == 0 {
		return nil
	}
	adapters := make([]DebugAdapter, n)
	for i := range adapters {
		a := &adapters[i]
		a.ID, a.Label, a.Command = d.str(), d.str(), d.str()
		if n := d.count(); n > 0 {
			a.Args = make([]string, n)
			for j := range a.Args {
				a.Args[j] = d.str()
			}
		}
	}
	return adapters
}
