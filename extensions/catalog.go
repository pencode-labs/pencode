package extensions

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pencode-labs/pencode/client"
	"golang.org/x/mod/semver"
)

const DefaultMarketplaceURL = "https://pencode-labs.github.io/extensions/extensions.json"

// Marketplace's zero value uses the official catalog and public GitHub API.
type Marketplace struct {
	Client      *http.Client
	Source, API string
}

type Listing struct {
	Name                          string `json:"name"`
	Repository                    string `json:"repository"`
	Description                   string `json:"description"`
	Version, URL, Digest, Problem string
}

func marketVersion(version string) string {
	if !strings.HasPrefix(version, "v") {
		return "v" + version
	}
	return version
}

func (l *Listing) NewerThan(version string) bool {
	latest, installed := marketVersion(l.Version), marketVersion(version)
	return semver.IsValid(latest) && semver.IsValid(installed) && semver.Compare(latest, installed) > 0
}

func (m Marketplace) get(ctx context.Context, address string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Pencode-Extensions")
	c := m.Client
	if c == nil {
		c = &http.Client{Timeout: 2 * time.Minute}
	}
	response, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("请求失败（HTTP %d）：%s", response.StatusCode, address)
	}
	return response, nil
}

func (m Marketplace) readJSON(ctx context.Context, address string, target any) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	response, err := m.get(ctx, address)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	const limit = 2 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return err
	}
	if len(data) > limit {
		return fmt.Errorf("扩展市场元数据超过 2 MiB")
	}
	return json.Unmarshal(data, target)
}

// List checks release metadata only; packages are downloaded by an explicit Install.
func (m Marketplace) List(ctx context.Context, goos, arch string) ([]Listing, error) {
	source := m.Source
	if source == "" {
		source = DefaultMarketplaceURL
	}
	var catalog struct {
		Extensions []Listing `json:"extensions"`
	}
	if err := m.readJSON(ctx, source, &catalog); err != nil {
		return nil, err
	}
	if len(catalog.Extensions) > 256 {
		return nil, fmt.Errorf("扩展市场条目过多")
	}
	seen := make(map[string]bool, len(catalog.Extensions))
	for i := range catalog.Extensions {
		l := &catalog.Extensions[i]
		if !fs.ValidPath(l.Name) || l.Name == "." || strings.ContainsAny(l.Name, "/\\:\x00") || seen[l.Name] {
			return nil, fmt.Errorf("无效或重复的扩展名称：%q", l.Name)
		}
		seen[l.Name] = true
		// Only release metadata may supply version and download information.
		l.Version, l.URL, l.Digest, l.Problem = "", "", "", ""
	}
	var workers sync.WaitGroup
	jobs := make(chan *Listing)
	for range min(4, len(catalog.Extensions)) {
		workers.Go(func() {
			for l := range jobs {
				if err := m.release(ctx, l, goos, arch); err != nil {
					l.Problem = err.Error()
				}
			}
		})
	}
	for i := range catalog.Extensions {
		select {
		case jobs <- &catalog.Extensions[i]:
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return nil, ctx.Err()
		}
	}
	close(jobs)
	workers.Wait()
	return catalog.Extensions, ctx.Err()
}

func (m Marketplace) release(ctx context.Context, l *Listing, goos, arch string) error {
	repository, err := url.Parse(l.Repository)
	if err != nil || repository.Scheme != "https" || repository.Host != "github.com" || repository.RawQuery != "" || repository.Fragment != "" {
		return fmt.Errorf("无效的 GitHub 仓库：%s", l.Repository)
	}
	parts := strings.Split(strings.Trim(repository.Path, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || parts[0] == "." || parts[0] == ".." || parts[1] == "." || parts[1] == ".." {
		return fmt.Errorf("无效的 GitHub 仓库：%s", l.Repository)
	}
	api := m.API
	if api == "" {
		api = "https://api.github.com"
	}
	var release struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if err := m.readJSON(ctx, strings.TrimRight(api, "/")+"/repos/"+strings.Join(parts, "/")+"/releases/latest", &release); err != nil {
		return err
	}
	if !semver.IsValid(marketVersion(release.Tag)) {
		return fmt.Errorf("无效的发布版本：%s", release.Tag)
	}
	l.Version = strings.TrimPrefix(release.Tag, "v")
	name := l.Name + "-" + release.Tag + "-" + goos + "-" + arch + ".zip"
	for _, asset := range release.Assets {
		if asset.Name == name {
			if !strings.HasPrefix(asset.URL, "https://") {
				return fmt.Errorf("无效的下载地址")
			}
			l.URL, l.Digest = asset.URL, asset.Digest
			return nil
		}
	}
	return fmt.Errorf("此版本没有适用于 %s-%s 的安装包", goos, arch)
}

// Install downloads and extracts a release into disposable local staging, then
// validates and publishes it through the workspace server's transaction.
func (m Marketplace) Install(ctx context.Context, backend *client.Client, l Listing, update bool) (Installed, *Bundle, error) {
	directory, err := os.MkdirTemp("", "pencode-extension-")
	if err != nil {
		return Installed{}, nil, err
	}
	defer os.RemoveAll(directory)
	source, err := m.download(ctx, directory, l)
	if err != nil {
		return Installed{}, nil, err
	}
	replace := ""
	if update {
		replace = l.Name
	}
	return install(ctx, backend, source, replace)
}

func (m Marketplace) download(ctx context.Context, directory string, l Listing) (string, error) {
	if l.Problem != "" || l.URL == "" {
		return "", fmt.Errorf("扩展没有可用的安装包")
	}
	response, err := m.get(ctx, l.URL)
	if err != nil {
		return "", fmt.Errorf("下载失败：%w", err)
	}
	defer response.Body.Close()
	archive, err := os.CreateTemp(directory, "download-*.zip")
	if err != nil {
		return "", err
	}
	defer archive.Close()
	const limit = 256 << 20
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(archive, hash), io.LimitReader(response.Body, limit+1))
	if err != nil {
		return "", err
	}
	if size > limit {
		return "", fmt.Errorf("扩展压缩包超过 256 MiB")
	}
	if l.Digest != "" && l.Digest != "sha256:"+hex.EncodeToString(hash.Sum(nil)) {
		return "", fmt.Errorf("扩展压缩包 SHA-256 校验失败")
	}
	reader, err := zip.NewReader(archive, size)
	if err != nil {
		return "", err
	}
	if len(reader.File) > 4096 {
		return "", fmt.Errorf("扩展文件数量超过 4096")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", err
	}
	defer root.Close()
	var total int64
	for _, file := range reader.File {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		name := strings.TrimSuffix(file.Name, "/")
		if !fs.ValidPath(name) || strings.ContainsAny(name, "\\:\x00") || !strings.HasPrefix(name, l.Name+"/") && name != l.Name {
			return "", fmt.Errorf("扩展压缩包路径无效：%s", file.Name)
		}
		if file.Mode().IsDir() {
			if err := root.MkdirAll(name, 0o755); err != nil {
				return "", err
			}
			continue
		}
		if !file.Mode().IsRegular() || file.UncompressedSize64 > uint64(limit-total) {
			return "", fmt.Errorf("扩展包含特殊文件或解压后超过 256 MiB")
		}
		if err := root.MkdirAll(path.Dir(name), 0o755); err != nil {
			return "", err
		}
		if err := extractMarketFile(root, file, name, limit-total); err != nil {
			return "", err
		}
		total += int64(file.UncompressedSize64)
	}
	data, err := readResource(root.FS(), l.Name+"/resources/extension.json", 64<<10)
	if err != nil {
		return "", err
	}
	if len(data) > 64<<10 {
		return "", fmt.Errorf("扩展清单超过 64 KiB")
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", err
	}
	if manifest.ID != l.Name || semver.Compare(marketVersion(manifest.Version), marketVersion(l.Version)) != 0 || !semver.IsValid(marketVersion(manifest.Version)) {
		return "", fmt.Errorf("扩展清单与发布的 ID 或版本不一致")
	}
	return filepath.Join(directory, l.Name), nil
}

func extractMarketFile(root *os.Root, file *zip.File, name string, limit int64) error {
	input, err := file.Open()
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, file.Mode().Perm()&0o755|0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, io.LimitReader(input, limit+1))
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
