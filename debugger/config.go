package debugger

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"path"
	"strconv"
	"strings"

	"github.com/pencode-labs/pencode/protocol"
)

// BuiltinTemplates contains the launchers for built-in debug adapters.
//
//go:embed templates.json
var BuiltinTemplates string

// Config keeps the editor's adapter settings separate from the opaque DAP
// launch/attach payload. All paths are interpreted on the workspace machine.
type Config struct {
	Port          *uint16         `json:"port,omitempty"`
	Label         string          `json:"label"`
	Adapter       string          `json:"adapter"`
	Command       string          `json:"command,omitempty"`
	Address       string          `json:"address,omitempty"`
	Host          string          `json:"host,omitempty"`
	Request       string          `json:"request"`
	Args          []string        `json:"args,omitempty"`
	Configuration json.RawMessage `json:"configuration"`
}

// AdapterCommand selects the transport on the workspace host. The DAP
// initialize request still identifies the adapter by its configured name.
func (c Config) AdapterCommand(override string) protocol.DebugAdapterCommand {
	if c.Port != nil {
		host := c.Host
		if host == "" {
			host = "127.0.0.1"
		}
		return protocol.DebugAdapterCommand{Adapter: "tcp", Address: net.JoinHostPort(host, strconv.Itoa(int(*c.Port)))}
	}
	if c.Address != "" {
		return protocol.DebugAdapterCommand{Adapter: "tcp", Address: c.Address}
	}
	return protocol.DebugAdapterCommand{Adapter: c.Adapter, Command: c.Command, Args: c.Args, Override: override}
}

func (c Config) validateTransport() error {
	switch {
	case c.Address != "" && (c.Host != "" || c.Port != nil):
		return fmt.Errorf("use either address or host/port")
	case c.Port != nil && *c.Port == 0:
		return fmt.Errorf("port must be between 1 and 65535")
	case c.Host != "" && c.Port == nil:
		return fmt.Errorf("host requires port")
	case c.Host != "" && !protocol.ValidPortHost(c.Host):
		return fmt.Errorf("invalid host %q", c.Host)
	case c.Address != "" || c.Port != nil:
		if c.Adapter == "stdio" || c.Command != "" || len(c.Args) != 0 {
			return fmt.Errorf("a TCP endpoint cannot be combined with stdio, command or args")
		}
	case c.Adapter == "tcp":
		return fmt.Errorf("tcp requires address or port")
	case c.Adapter == "stdio" && c.Command == "":
		return fmt.Errorf("stdio requires command")
	}
	return nil
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
		if err := c.validateTransport(); err != nil {
			return nil, fmt.Errorf("debug.json: %q: %w", c.Label, err)
		}
		if err := c.replaceStrings(func(value string) string { return strings.ReplaceAll(value, "${workspaceFolder}", root) }); err != nil {
			return nil, err
		}
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
	replacer := strings.NewReplacer("${file}", filename, "${fileDirname}", path.Dir(filename))
	err := c.replaceStrings(replacer.Replace)
	return c, err
}

func (c *Config) replaceStrings(replace func(string) string) error {
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(c.Configuration))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil || value == nil {
		return fmt.Errorf("debug.json: configuration must be an object")
	}
	var err error
	c.Configuration, err = json.Marshal(resolveValue(value, replace))
	return err
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
