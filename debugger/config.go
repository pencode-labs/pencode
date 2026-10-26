package debugger

import (
	"encoding/json"
	"fmt"
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
	if len(configs) == 0 {
		return nil, fmt.Errorf("debug.json: no configurations")
	}
	for i := range configs {
		c := &configs[i]
		if c.Label == "" || (c.Request != "launch" && c.Request != "attach") {
			return nil, fmt.Errorf("debug.json: label and launch/attach request are required")
		}
		if c.Adapter != "Delve" && c.Adapter != "stdio" && c.Adapter != "tcp" {
			return nil, fmt.Errorf("debug.json: unknown adapter %q", c.Adapter)
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
		c.Configuration, _ = json.Marshal(resolveValue(value, root))
	}
	return configs, nil
}

func resolveValue(value any, root string) any {
	switch v := value.(type) {
	case string:
		return strings.ReplaceAll(v, "${workspaceFolder}", root)
	case map[string]any:
		for key, item := range v {
			v[key] = resolveValue(item, root)
		}
	case []any:
		for i := range v {
			v[i] = resolveValue(v[i], root)
		}
	}
	return value
}
