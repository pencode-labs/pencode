// Package remote installs and starts a headless server through system OpenSSH.
// Authentication, SSH config, agent and known_hosts belong to OpenSSH.
package remote

import (
	"bufio"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/diiyw/onix/projects/pencode/transport"
)

type Target struct {
	Host         string `json:"host"`
	Port         uint16 `json:"port,omitempty"`
	IdentityFile string `json:"identityFile,omitempty"`
	ConfigFile   string `json:"configFile,omitempty"`
	ServerBinary string `json:"serverBinary,omitempty"` // Explicit local transfer source.
	ServerRoot   string `json:"serverRoot,omitempty"`   // Absolute POSIX path on the remote host.
}

func (t Target) Validate() error {
	if t.Host == "" || strings.HasPrefix(t.Host, "-") || strings.ContainsAny(t.Host, " \t\r\n\x00/\\'\"`$;&|<>(){}") {
		return fmt.Errorf("请输入 SSH Host 别名或 user@host")
	}
	for _, value := range [...]string{t.IdentityFile, t.ConfigFile, t.ServerBinary, t.ServerRoot} {
		if strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("无效的 SSH 文件路径")
		}
	}
	if t.ServerRoot != "" && !strings.HasPrefix(t.ServerRoot, "/") {
		return fmt.Errorf("远端服务安装目录必须是绝对路径")
	}
	return nil
}

type Platform struct{ OS, Arch string }

func (p Platform) String() string { return p.OS + "-" + p.Arch }

type Connector struct {
	Executable      string // Empty selects ssh from PATH.
	ServerDirectory string // resources/servers/<os>-<arch>/pencode-server.
	LocalServer     string // Adjacent native standalone binary.
	Output          io.Writer
}

func (c Connector) arguments(t Target, command string, interactive bool) []string {
	batch, hostCheck := "BatchMode=yes", "StrictHostKeyChecking=yes"
	if interactive {
		batch, hostCheck = "BatchMode=no", "StrictHostKeyChecking=ask"
	}
	args := []string{"-T", "-o", batch, "-o", "AddKeysToAgent=no", "-o", "NumberOfPasswordPrompts=3", "-o", "ConnectTimeout=10", "-o", "ConnectionAttempts=1",
		"-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3", "-o", hostCheck,
		"-o", "ClearAllForwardings=yes", "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "LogLevel=ERROR"}
	if t.Port != 0 {
		args = append(args, "-p", strconv.Itoa(int(t.Port)))
	}
	if t.IdentityFile != "" {
		args = append(args, "-i", t.IdentityFile)
	}
	if t.ConfigFile != "" {
		args = append(args, "-F", t.ConfigFile)
	}
	return append(args, "--", t.Host, "sh -c "+shellQuote(command))
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

const platformCommand = `set -eu
printf 'pencode-ssh-v1\n'
uname -s
uname -m`

func parsePlatform(data string) (Platform, error) {
	lines := strings.Split(strings.TrimSpace(data), "\n")
	if len(lines) != 3 || lines[0] != "pencode-ssh-v1" {
		return Platform{}, fmt.Errorf("SSH 启动输出无效；远端 shell 的 stdout 必须保持安静")
	}
	var platform Platform
	switch strings.TrimSpace(lines[1]) {
	case "Linux":
		platform.OS = "linux"
	case "Darwin":
		platform.OS = "darwin"
	default:
		return platform, fmt.Errorf("远端系统 %q 暂不支持安装", lines[1])
	}
	switch strings.TrimSpace(lines[2]) {
	case "x86_64", "amd64":
		platform.Arch = "amd64"
	case "aarch64", "arm64":
		platform.Arch = "arm64"
	default:
		return platform, fmt.Errorf("远端架构 %q 暂不支持", lines[2])
	}
	return platform, nil
}

func (c Connector) artifact(t Target, platform Platform) (*os.File, string, error) {
	name := t.ServerBinary
	if name == "" {
		name = filepath.Join(c.ServerDirectory, platform.String(), "pencode-server")
		if _, err := os.Stat(name); os.IsNotExist(err) && platform.OS == runtime.GOOS && platform.Arch == runtime.GOARCH {
			name = c.LocalServer
		}
	}
	info, err := buildinfo.ReadFile(name)
	if err != nil {
		return nil, "", fmt.Errorf("需要 %s 的独立 pencode-server（%s）：%w", platform, name, err)
	}
	var target Platform
	for _, setting := range info.Settings {
		switch setting.Key {
		case "GOOS":
			target.OS = setting.Value
		case "GOARCH":
			target.Arch = setting.Value
		}
	}
	if target != platform || info.Path != "github.com/diiyw/onix/projects/pencode/cmd/pencode-server" {
		return nil, "", fmt.Errorf("服务端程序必须是为 %s 构建的独立 pencode-server", platform)
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, "", err
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, f); err == nil {
		_, err = f.Seek(0, io.SeekStart)
	}
	if err != nil {
		f.Close()
		return nil, "", err
	}
	return f, hex.EncodeToString(hash.Sum(nil)), nil
}

func installBase(target Target) string {
	if target.ServerRoot != "" {
		return shellQuote(target.ServerRoot)
	}
	return `"$HOME/.pencode"`
}

// bootstrapCommand keeps authentication, installation and the protocol on one
// SSH session. Installed commands are reused before requesting a local binary.
func bootstrapCommand(target Target) string {
	return `set -eu
if command -v pencode-server >/dev/null 2>&1; then
  printf 'installed\nstarted\n'
  exec pencode-server
fi
if command -v pencode >/dev/null 2>&1; then
  printf 'installed\nstarted\n'
  exec pencode --pencode-server
fi
base=` + installBase(target) + `
server="$base/pencode-server"
if [ -f "$server" ] && [ -x "$server" ]; then
  printf 'installed\nstarted\n'
  exec "$server"
fi
` + platformCommand + `
IFS= read -r digest
IFS= read -r size
case "$digest" in ''|*[!0-9a-f]*) exit 1;; esac
[ "${#digest}" = 64 ]
case "$size" in ''|*[!0-9]*) exit 1;; esac
checksum() {
  case "$(uname -s)" in Darwin) shasum -a 256;; *) sha256sum;; esac
}
printf 'missing\n'
umask 077
mkdir -p "$base"
stage=$(mktemp -d "$base/.install.XXXXXXXX")
trap 'rm -rf "$stage"' EXIT HUP INT TERM
head -c "$size" > "$stage/pencode-server"
[ "$(wc -c < "$stage/pencode-server" | tr -d ' ')" = "$size" ]
actual=$(checksum < "$stage/pencode-server")
[ "${actual%% *}" = "$digest" ] || exit 1
chmod 700 "$stage/pencode-server"
mv -f "$stage/pencode-server" "$server"
rm -rf "$stage"
trap - EXIT HUP INT TERM
printf 'started\n'
exec "$server"`
}

// Connect authenticates once and returns the same encrypted stdio stream after
// bootstrap. Passwords are requested through askpass and never retained here.
func (c Connector) Connect(ctx context.Context, target Target, progress func(string)) (io.ReadWriteCloser, error) {
	if err := target.Validate(); err != nil {
		return nil, err
	}
	if c.Executable == "" {
		c.Executable = "ssh"
	}
	env, closePrompt, err := startAskpass(ctx)
	if err != nil {
		return nil, err
	}
	defer closePrompt()
	report := func(message string) {
		if progress != nil {
			progress(message)
		}
	}
	report("正在连接 SSH 并检测远端服务…")
	diagnostics := &boundedOutput{output: c.Output}
	stream, err := transport.StartPeerWithEnv(c.Executable, diagnostics, env, c.arguments(target, bootstrapCommand(target), env != nil)...)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = stream.Close() })
	success := false
	defer func() {
		stop()
		if !success {
			_ = stream.Close()
		}
	}()
	reader := bufio.NewReaderSize(stream, 4096)
	line := func() (string, error) {
		value, err := reader.ReadSlice('\n')
		if err != nil {
			return "", fmt.Errorf("SSH %s: %w: %s", target.Host, err, diagnostics.String())
		}
		return string(value), nil
	}
	status, err := line()
	if err != nil {
		return nil, err
	}
	if status == "installed\n" {
		report("使用远端已安装的服务端…")
	} else {
		var platformData strings.Builder
		platformData.WriteString(status)
		for range 2 {
			value, err := line()
			if err != nil {
				return nil, err
			}
			platformData.WriteString(value)
		}
		platform, err := parsePlatform(platformData.String())
		if err != nil {
			return nil, err
		}
		report("正在校验 " + platform.String() + " 服务端…")
		file, digest, err := c.artifact(target, platform)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return nil, err
		}
		if _, err := fmt.Fprintf(stream, "%s\n%d\n", digest, info.Size()); err != nil {
			return nil, err
		}
		status, err = line()
		if err != nil {
			return nil, err
		}
		if status != "missing\n" {
			return nil, fmt.Errorf("服务端校验响应无效")
		}
		report("正在上传并安装 pencode-server…")
		if _, err := io.CopyN(stream, file, info.Size()); err != nil {
			return nil, err
		}
	}
	report("正在启动远端服务…")
	if status, err := line(); err != nil {
		return nil, err
	} else if status != "started\n" {
		return nil, fmt.Errorf("服务端启动响应无效")
	}
	if !stop() || ctx.Err() != nil {
		return nil, ctx.Err()
	}
	success = true
	return &sshStream{ReadWriteCloser: stream, reader: reader, diagnostics: diagnostics, host: target.Host}, nil
}

type sshStream struct {
	io.ReadWriteCloser
	reader      *bufio.Reader
	diagnostics *boundedOutput
	host        string
}

func (s *sshStream) Read(data []byte) (int, error) {
	n, err := s.reader.Read(data)
	if err != nil && err != io.EOF {
		return n, fmt.Errorf("SSH %s: %w: %s", s.host, err, s.diagnostics.String())
	}
	if err == io.EOF {
		if diagnostics := s.diagnostics.String(); diagnostics != "" {
			err = fmt.Errorf("SSH %s: %w: %s", s.host, err, diagnostics)
		}
	}
	return n, err
}

// Continue draining arbitrary diagnostics while retaining at most 4 KiB.
type boundedOutput struct {
	mu     sync.Mutex
	data   [4096]byte
	used   int
	output io.Writer
}

func (b *boundedOutput) Write(data []byte) (int, error) {
	b.mu.Lock()
	b.used += copy(b.data[b.used:], data)
	b.mu.Unlock()
	if b.output != nil {
		_, _ = b.output.Write(data)
	}
	return len(data), nil
}

func (b *boundedOutput) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data[:b.used])
}
