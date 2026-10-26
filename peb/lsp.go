package peb

// LanguageServer contributes a standard stdio LSP server. Command is an
// executable from PATH, an absolute path, or an extension-relative path.
// JSON options are carried verbatim; the host validates them before activation.
type LanguageServer struct {
	ID, Label, LanguageID, Command       string
	Args                                 []string
	Selectors                            []DocumentSelector
	InitializationOptions, Configuration string
}

func encodeLanguageServers(e *encoder, servers []LanguageServer) {
	e.count(len(servers))
	for _, s := range servers {
		e.str(s.ID)
		e.str(s.Label)
		e.str(s.LanguageID)
		e.str(s.Command)
		e.count(len(s.Args))
		for _, arg := range s.Args {
			e.str(arg)
		}
		e.count(len(s.Selectors))
		for _, rule := range s.Selectors {
			e.number(uint32(rule.Match))
			e.str(rule.Pattern)
			e.str(rule.LanguageID)
		}
		e.text(s.InitializationOptions)
		e.text(s.Configuration)
	}
}
func decodeLanguageServers(d *decoder) []LanguageServer {
	n := d.count()
	if n == 0 {
		return nil
	}
	servers := make([]LanguageServer, n)
	for i := range servers {
		s := &servers[i]
		s.ID, s.Label, s.LanguageID, s.Command = d.str(), d.str(), d.str(), d.str()
		if n := d.count(); n > 0 {
			s.Args = make([]string, n)
			for j := range s.Args {
				s.Args[j] = d.str()
			}
		}
		s.Selectors = make([]DocumentSelector, d.count())
		for j := range s.Selectors {
			s.Selectors[j] = DocumentSelector{Match: Match(d.number()), Pattern: d.str(), LanguageID: d.str()}
		}
		s.InitializationOptions, s.Configuration = d.text(), d.text()
	}
	return servers
}
