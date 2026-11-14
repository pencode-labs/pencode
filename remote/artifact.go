package remote

import (
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type serverArtifact struct {
	file      *os.File
	digest    string
	temporary bool
}

func (a *serverArtifact) Close() {
	a.file.Close()
	if a.temporary {
		os.Remove(a.file.Name())
	}
}

func (c Connector) artifact(ctx context.Context, t Target, platform Platform) (_ *serverArtifact, err error) {
	a := &serverArtifact{}
	if t.ServerBinary == "" && c.ServerDirectory == "" && c.Version != "dev" {
		a.file, a.digest, err = c.download(ctx, platform)
		a.temporary = true
	} else {
		name := t.ServerBinary
		if name == "" {
			name = filepath.Join(c.ServerDirectory, platform.String(), "pencode-server")
			if _, err := os.Stat(name); os.IsNotExist(err) && platform.OS == runtime.GOOS && platform.Arch == runtime.GOARCH {
				name = c.LocalServer
			}
		}
		a.file, err = os.Open(name)
	}
	if err != nil {
		return nil, fmt.Errorf("获取 pencode-server %s（%s）失败：%w", c.Version, platform, err)
	}
	defer func() {
		if err != nil {
			a.Close()
		}
	}()
	info, err := buildinfo.Read(a.file)
	if err != nil {
		return nil, fmt.Errorf("需要 %s 的独立 pencode-server：%w", platform, err)
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
	if target != platform || info.Path != "github.com/pencode-labs/pencode/cmd/pencode-server" {
		return nil, fmt.Errorf("服务端程序必须是为 %s 构建的独立 pencode-server", platform)
	}
	if a.digest == "" {
		hash := sha256.New()
		if _, err = io.Copy(hash, a.file); err != nil {
			return nil, err
		}
		a.digest = hex.EncodeToString(hash.Sum(nil))
	}
	if _, err = a.file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return a, nil
}

func (c Connector) releaseAddress(platform Platform) string {
	base := c.ReleaseURL
	if base == "" {
		base = "https://github.com/pencode-labs/pencode/releases/download"
	}
	return strings.TrimRight(base, "/") + "/" + c.Version + "/pencode-server-" + c.Version + "-" + platform.String()
}

func (c Connector) get(ctx context.Context, address string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "pencode-ssh")
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("下载 %s：HTTP %d", address, resp.StatusCode)
	}
	return resp, nil
}

// Download on the client so remote hosts do not need internet access or curl.
func (c Connector) download(ctx context.Context, platform Platform) (file *os.File, digest string, err error) {
	address := c.releaseAddress(platform)
	name := "pencode-server-" + c.Version + "-" + platform.String()
	resp, err := c.get(ctx, strings.TrimSuffix(address, name)+"SHA256SUMS")
	if err != nil {
		return nil, "", err
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	resp.Body.Close()
	if err != nil {
		return nil, "", err
	}
	if len(data) > 1<<20 {
		return nil, "", fmt.Errorf("服务端发行包缺少有效 SHA-256 校验值")
	}
	var checksum string
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == name {
			checksum = fields[0]
			break
		}
	}
	expected, err := hex.DecodeString(checksum)
	if err != nil || len(expected) != sha256.Size {
		return nil, "", fmt.Errorf("服务端发行包缺少有效 SHA-256 校验值")
	}
	resp, err = c.get(ctx, address)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	file, err = os.CreateTemp("", "pencode-server-*")
	if err != nil {
		return nil, "", err
	}
	defer func() {
		if err != nil {
			file.Close()
			os.Remove(file.Name())
		}
	}()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(resp.Body, (128<<20)+1))
	if err != nil {
		return file, "", err
	}
	digest = hex.EncodeToString(hash.Sum(nil))
	if n > 128<<20 || !strings.EqualFold(digest, checksum) {
		return file, "", fmt.Errorf("服务端发行包 SHA-256 校验失败")
	}
	return file, digest, nil
}
