// Package connection selects local, SSH or WebSocket transports and reopens read handles.
// It never retries or replays filesystem mutations.
package connection

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/protocol"
	"github.com/pencode-labs/pencode/remote"
	"github.com/pencode-labs/pencode/transport"
)

type Profile struct {
	Name      string                 `json:"name"`
	SSH       remote.Target          `json:"ssh"`
	WebSocket remote.WebSocketTarget `json:"websocket,omitzero"`
	Directory string                 `json:"directory,omitempty"`
}

func (p Profile) Remote() bool { return p.SSH.Host != "" || p.WebSocket != (remote.WebSocketTarget{}) }

type Endpoint struct {
	SSH       remote.Target
	WebSocket string
}

// Endpoint identifies a connection without allocating or retaining its token.
func (p Profile) Endpoint() Endpoint { return Endpoint{p.SSH, p.WebSocket.Address} }

func (p Profile) Address() string {
	if p.WebSocket.Address != "" {
		return p.WebSocket.Address
	}
	return p.SSH.Host
}

func (p Profile) Validate() error {
	if p.WebSocket != (remote.WebSocketTarget{}) {
		if p.SSH != (remote.Target{}) {
			return fmt.Errorf("一个连接只能选择 SSH 或 WebSocket")
		}
		return p.WebSocket.Validate()
	}
	return p.SSH.Validate()
}

// SameHost excludes display names, directories and credentials from history identity.
func (p Profile) SameHost(other Profile) bool {
	return p.WebSocket.Address == other.WebSocket.Address && p.SSH.Host == other.SSH.Host && p.SSH.Port == other.SSH.Port && p.SSH.ConfigFile == other.SSH.ConfigFile
}

func (p Profile) Label() string {
	if !p.Remote() {
		return "本地"
	}
	if p.Name != "" {
		return p.Name
	}
	return p.Address()
}

type Profiles struct {
	Connections []Profile `json:"connections"`
}

// Namespace prevents identical remote and local paths sharing history entries.
func (p Profile) Namespace() string {
	if !p.Remote() {
		return ""
	}
	if p.WebSocket.Address != "" {
		return p.WebSocket.Address + "|"
	}
	return fmt.Sprintf("ssh://%s:%d/%s|", p.SSH.Host, p.SSH.Port, p.SSH.ConfigFile)
}

type Dialer struct {
	LocalExecutable string
	SSH             remote.Connector
}

func DefaultDialer(executable string, output io.Writer) Dialer {
	directory := filepath.Dir(executable)
	server := "pencode-server"
	if runtime.GOOS == "windows" {
		server += ".exe"
	}
	return Dialer{LocalExecutable: executable, SSH: remote.Connector{
		ServerDirectory: filepath.Join(directory, "resources", "servers"),
		LocalServer:     filepath.Join(directory, server), Output: output,
	}}
}

func (d Dialer) Dial(ctx context.Context, profile Profile, progress func(string)) (*client.Client, error) {
	var stream io.ReadWriteCloser
	var err error
	if profile.Remote() {
		if err := profile.Validate(); err != nil {
			return nil, err
		}
	}
	if profile.WebSocket.Address != "" {
		stream, err = remote.ConnectWebSocket(ctx, profile.WebSocket, progress)
	} else if profile.Remote() {
		stream, err = d.SSH.Connect(ctx, profile.SSH, progress)
	} else {
		if progress != nil {
			progress("正在启动本地服务…")
		}
		stream, err = transport.StartPeerWithOutput(d.LocalExecutable, d.SSH.Output, "--pencode-server")
	}
	if err != nil {
		return nil, err
	}
	return client.New(ctx, stream)
}

type File struct {
	Path string
	Text bool
}

type Project struct {
	Path  string
	Files []File
}

type RestoredFile struct {
	Document *client.Document
	Disk     string
	Err      error
}

type RestoredProject struct {
	Workspace protocol.Workspace
	Files     []RestoredFile
	Err       error
}

// Restore only opens workspaces and reads disk baselines. The foreground decides
// whether its current unsaved buffer can be attached to each fresh document.
func Restore(ctx context.Context, backend *client.Client, projects []Project) []RestoredProject {
	results := make([]RestoredProject, len(projects))
	for i, project := range projects {
		r := &results[i]
		r.Workspace, r.Err = backend.OpenWorkspace(ctx, project.Path)
		if r.Err != nil {
			continue
		}
		r.Files = make([]RestoredFile, len(project.Files))
		for j, file := range project.Files {
			r.Files[j].Document, r.Files[j].Disk, r.Files[j].Err = backend.OpenDocument(ctx, r.Workspace.ID, file.Path, file.Text)
		}
	}
	return results
}

func ProfilePath() string {
	directory, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(directory, "pencode", "resources", "connection.json")
}

func LoadProfiles(name string) []Profile {
	var profiles Profiles
	f, err := os.Open(name)
	if err != nil {
		return nil
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if err != nil || len(data) > 64<<10 || json.Unmarshal(data, &profiles) != nil {
		return nil
	}
	return profiles.Connections
}

func SaveProfiles(name string, profiles []Profile) error {
	if name == "" {
		return nil
	}
	data, err := json.MarshalIndent(Profiles{Connections: profiles}, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > 64<<10 {
		return fmt.Errorf("连接配置超过 64 KiB 预算")
	}
	directory := filepath.Dir(name)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(directory, ".connection-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), name)
}
