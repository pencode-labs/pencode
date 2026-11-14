// Package remote connects to headless servers through SSH or WebSocket.
// SSH authentication, config, agent and known_hosts belong to OpenSSH.
package remote

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/pencode-labs/pencode/internal/version"
	"github.com/pencode-labs/pencode/transport"
)

const OutputChannelName = "Remote Development"

type Target struct {
	Host         string `json:"host"`
	IdentityFile string `json:"identityFile,omitempty"`
	ConfigFile   string `json:"configFile,omitempty"`
	ServerBinary string `json:"serverBinary,omitempty"` // Explicit local transfer source.
	ServerRoot   string `json:"serverRoot,omitempty"`   // Absolute POSIX path on the remote host.
	Port         uint16 `json:"port,omitempty"`
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
	Output          io.Writer
	HTTP            *http.Client // Nil uses http.DefaultClient with the connection context.
	Executable      string       // Empty selects ssh from PATH.
	Version         string       // Empty selects the current build version.
	ReleaseURL      string       // Empty selects GitHub release downloads.
	ServerDirectory string       // resources/servers/<os>-<arch>/pencode-server.
	LocalServer     string       // Adjacent native standalone binary.
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
	// SSH commands do not normally load the login environment. Use the user's
	// shell so workspace tools inherit PATH, then run the POSIX bootstrap in sh.
	command = `exec "${SHELL:-/bin/sh}" -lc ` + shellQuote("exec sh -c "+shellQuote(command))
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

func installBase(target Target) string {
	if target.ServerRoot != "" {
		return shellQuote(target.ServerRoot)
	}
	return `"$HOME/.pencode"`
}

// bootstrapCommand keeps authentication, installation and the protocol on one
// SSH session. Only a server reporting the exact client version is reused.
func bootstrapCommand(target Target, version string) string {
	return `set -eu
expected=` + shellQuote(version) + `
base=` + installBase(target) + `
server="$base/pencode-server"
reuse() {
  [ -f "$1" ] && [ -x "$1" ] || return 0
  actual=$("$1" --version 2>/dev/null) || actual=unknown
  if [ "$actual" = "$expected" ]; then
    printf '[SSH] 使用 %s，版本 %s\n' "$1" "$actual" >&2
    printf 'installed\nstarted\n'
    exec "$1"
  fi
  printf '[SSH] %s 版本不匹配（当前 %s，需要 %s）\n' "$1" "$actual" "$expected" >&2
}
reuse "$server"
if candidate=$(command -v pencode-server); then
  reuse "$candidate"
fi
printf '[SSH] 未找到匹配的 pencode-server %s，准备安装\n' "$expected" >&2
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
[ "$("$stage/pencode-server" --version)" = "$expected" ] || { printf '[SSH] 下载的服务端版本不匹配\n' >&2; exit 1; }
mv -f "$stage/pencode-server" "$server"
rm -rf "$stage"
trap - EXIT HUP INT TERM
printf '[SSH] 已安装 %s，版本 %s\n' "$server" "$expected" >&2
printf 'started\n'
exec "$server"`
}

// Connect authenticates once and returns the same encrypted stdio stream after
// bootstrap. Passwords are requested through askpass and never retained here.
func (c Connector) Connect(ctx context.Context, target Target, progress func(string)) (conn io.ReadWriteCloser, err error) {
	defer func() {
		if err != nil && c.Output != nil {
			fmt.Fprintf(c.Output, "[SSH %s] 连接失败：%v\n", target.Host, err)
		}
	}()
	if err := target.Validate(); err != nil {
		return nil, err
	}
	if c.Version == "" {
		c.Version = version.Value
	}
	if !version.Valid(c.Version) {
		return nil, fmt.Errorf("无效的服务端版本 %q", c.Version)
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
		if c.Output != nil {
			fmt.Fprintf(c.Output, "[SSH %s] %s\n", target.Host, message)
		}
		if progress != nil {
			progress(message)
		}
	}
	report("正在连接 SSH 并检测 pencode-server " + c.Version + "…")
	diagnostics := &boundedOutput{output: c.Output}
	stream, err := transport.StartPeerWithEnv(c.Executable, diagnostics, env, c.arguments(target, bootstrapCommand(target, c.Version), env != nil)...)
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
		if target.ServerBinary == "" && c.ServerDirectory == "" && c.Version != "dev" {
			report("正在从 GitHub 下载 " + c.releaseAddress(platform) + "…")
		}
		artifact, err := c.artifact(ctx, target, platform)
		if err != nil {
			return nil, err
		}
		defer artifact.Close()
		report("服务端 SHA-256 校验通过：" + artifact.digest)
		info, err := artifact.file.Stat()
		if err != nil {
			return nil, err
		}
		if _, err := fmt.Fprintf(stream, "%s\n%d\n", artifact.digest, info.Size()); err != nil {
			return nil, err
		}
		status, err = line()
		if err != nil {
			return nil, err
		}
		if status != "missing\n" {
			return nil, fmt.Errorf("服务端校验响应无效")
		}
		report("正在上传并安装 pencode-server " + c.Version + "…")
		if _, err := io.CopyN(stream, artifact.file, info.Size()); err != nil {
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
	report("远端 pencode-server " + c.Version + " 已启动")
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
		if peer, ok := s.ReadWriteCloser.(interface{ Wait() error }); ok {
			if exit := peer.Wait(); exit != nil {
				err = fmt.Errorf("SSH %s: %w: %v: %s", s.host, err, exit, s.diagnostics.String())
			}
		}
	}
	return n, err
}

// Continue draining arbitrary diagnostics while retaining at most 4 KiB.
type boundedOutput struct {
	output io.Writer
	used   int
	mu     sync.Mutex
	data   [4096]byte
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
