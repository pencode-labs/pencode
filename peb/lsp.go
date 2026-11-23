package peb

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// LanguageServer contributes a standard stdio LSP server. Command is an
// executable from PATH, an absolute path, or an extension-relative path.
// Args can reference extension string settings using ${setting:id}.
// JSON options are carried verbatim; the host validates them before activation.
type LanguageServer struct {
	ID, Label, LanguageID, Command       string
	InitializationOptions, Configuration string
	Args                                 []string
	Selectors                            []DocumentSelector
}

// LaunchSetting returns the string setting referenced by an entire argument
// of the form ${setting:id}. Its value is passed as one argument.
func LaunchSetting(arg string) (string, bool) {
	id, ok := strings.CutPrefix(arg, "${setting:")
	if !ok {
		return "", false
	}
	return strings.CutSuffix(id, "}")
}

// LaunchArgs resolves settings against the registered schema.
func (s LanguageServer) LaunchArgs(settings []Setting, values SettingValues) ([]string, error) {
	var args []string
	for j, arg := range s.Args {
		id, ok := LaunchSetting(arg)
		if !ok {
			continue
		}
		i := slices.IndexFunc(settings, func(setting Setting) bool { return setting.ID == id })
		if i < 0 || settings[i].Type != SettingString {
			return nil, fmt.Errorf("unknown string launch setting %q", id)
		}
		value := values[id]
		if value == nil {
			value = settings[i].Default
		}
		if err := settings[i].Validate(value); err != nil {
			return nil, err
		}
		var resolved string
		if err := json.Unmarshal(value, &resolved); err != nil || strings.ContainsRune(resolved, 0) {
			return nil, fmt.Errorf("invalid launch setting %q", id)
		}
		if args == nil {
			args = slices.Clone(s.Args)
		}
		args[j] = resolved
	}
	if args == nil {
		return s.Args, nil
	}
	return args, nil
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
