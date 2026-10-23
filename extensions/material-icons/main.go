// material-icons registers the Material Design Icons file icon theme over peb.
package main

import (
	_ "embed"
	"fmt"
	"os"
	"strings"

	"github.com/diiyw/onix/projects/pencode/peb"
)

//go:embed resources/rules.psv
var ruleTable string

func iconRules(data string) ([]peb.IconRule, error) {
	var rules []peb.IconRule
	paths := make(map[string]string)
	for line := range strings.Lines(data) {
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			continue
		}
		if len(line) < 4 || line[0] < '0' || line[0] > '4' || line[1] != '\x1f' {
			return nil, fmt.Errorf("invalid icon rule %q", line)
		}
		pattern, icon, ok := strings.Cut(line[2:], "\x1f")
		if !ok || icon == "" || strings.ContainsRune(icon, '\x1f') {
			return nil, fmt.Errorf("invalid icon rule %q", line)
		}
		if len(rules) == peb.MaxItems {
			return nil, fmt.Errorf("icon rule table exceeds %d rules", peb.MaxItems)
		}
		path, exists := paths[icon]
		if !exists {
			path = "resources/icons/" + icon + ".svg"
			paths[icon] = path
		}
		rules = append(rules, peb.IconRule{Match: peb.Match(line[0] - '0'), Pattern: pattern, Path: path})
	}
	if len(rules) == 0 {
		return nil, fmt.Errorf("icon rule table is empty")
	}
	return rules, nil
}

func run() error {
	rules, err := iconRules(ruleTable)
	if err != nil {
		return err
	}
	return peb.Serve(os.Stdin, os.Stdout, peb.Contributions{
		IconThemes: []peb.IconTheme{{ID: "material-design-icons", Label: "Material Design Icons", Rules: rules}},
	})
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "material-icons:", err)
		os.Exit(1)
	}
}
