package debugger

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

// Config keeps the editor's adapter settings separate from the opaque DAP
// launch/attach payload. All paths are interpreted on the workspace machine.
type Config struct {
	Label         string          `json:"label"`
	Adapter       string          `json:"adapter"`
	Command       string          `json:"command,omitempty"`
	Args          []string        `json:"args,omitempty"`
	Address       string          `json:"address,omitempty"`
	Request       string          `json:"request"`
	Configuration json.RawMessage `json:"configuration"`
}

func ParseConfigs(data []byte, root string) ([]Config, error) {
	var configs []Config
	if err := json.Unmarshal(data, &configs); err != nil {
		return nil, fmt.Errorf("debug.json: %w", err)
	}
	if configs == nil {
		return nil, fmt.Errorf("debug.json: configurations must be an array")
	}
	for i := range configs {
		c := &configs[i]
		if c.Label == "" || (c.Request != "launch" && c.Request != "attach") {
			return nil, fmt.Errorf("debug.json: label and launch/attach request are required")
		}
		if c.Adapter == "" || len(c.Adapter) > 128 || strings.ContainsAny(c.Adapter, "\x00\r\n/\\") {
			return nil, fmt.Errorf("debug.json: invalid adapter %q", c.Adapter)
		}
		if c.Adapter == "stdio" && c.Command == "" || c.Adapter == "tcp" && c.Address == "" {
			return nil, fmt.Errorf("debug.json: adapter requires command/address")
		}
		// Substitute string values recursively, preserving JSON escaping and
		// numeric/boolean adapter options.
		var value map[string]any
		if err := json.Unmarshal(c.Configuration, &value); err != nil || value == nil {
			return nil, fmt.Errorf("debug.json: configuration must be an object")
		}
		c.Configuration, _ = json.Marshal(resolveValue(value, func(value string) string { return strings.ReplaceAll(value, "${workspaceFolder}", root) }))
	}
	return configs, nil
}

// ResolveFile expands the active workspace document at launch time so cached
// configurations follow file selection without changing the project JSON.
func (c Config) ResolveFile(filename string) (Config, error) {
	if !strings.Contains(string(c.Configuration), "${file}") && !strings.Contains(string(c.Configuration), "${fileDirname}") {
		return c, nil
	}
	if filename == "" {
		return c, fmt.Errorf("debug.json: open a file before launching a file configuration")
	}
	filename = strings.ReplaceAll(filename, "\\", "/")
	var value map[string]any
	if err := json.Unmarshal(c.Configuration, &value); err != nil {
		return c, err
	}
	replacer := strings.NewReplacer("${file}", filename, "${fileDirname}", path.Dir(filename))
	var err error
	c.Configuration, err = json.Marshal(resolveValue(value, replacer.Replace))
	return c, err
}

func resolveValue(value any, replace func(string) string) any {
	switch v := value.(type) {
	case string:
		return replace(v)
	case map[string]any:
		for key, item := range v {
			v[key] = resolveValue(item, replace)
		}
	case []any:
		for i := range v {
			v[i] = resolveValue(v[i], replace)
		}
	}
	return value
}
