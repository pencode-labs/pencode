// Package updater downloads verified desktop distributions into a sibling staging directory.
package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/mod/semver"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const VersionFile = "pencode-version"
const api = "https://api.github.com/repos/pencode-labs/pencode"

type httpStatus int

func (s httpStatus) Error() string { return fmt.Sprintf("GitHub HTTP %d", s) }

type Release struct {
	Tag       string
	URL       string
	Checksums string
	Name      string
}

type Client struct {
	HTTP *http.Client
	API  string
}

func NewClient() Client { return Client{HTTP: &http.Client{Timeout: 15 * time.Minute}, API: api} }

func Installation(executable string) (root, version string, err error) {
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return "", "", err
	}
	dir := filepath.Dir(executable)
	for range 5 {
		data, readErr := os.ReadFile(filepath.Join(dir, VersionFile))
		if readErr == nil {
			return dir, strings.TrimSpace(string(data)), nil
		}
		dir = filepath.Dir(dir)
	}
	return "", "dev", errors.New("当前不是支持自动更新的发行包，请先安装新版发行包")
}

func (c Client) get(ctx context.Context, address string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "pencode-updater")
	if strings.HasPrefix(address, c.API+"/") {
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, httpStatus(resp.StatusCode)
	}
	return resp, nil
}

// Check resolves tags to published releases with matching desktop assets.
func (c Client) Check(ctx context.Context, current string) (Release, error) {
	var best Release
	for page := 1; ; page++ {
		resp, err := c.get(ctx, fmt.Sprintf("%s/tags?per_page=100&page=%d", c.API, page))
		if err != nil {
			return best, err
		}
		var tags []struct {
			Name string `json:"name"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&tags)
		resp.Body.Close()
		if err != nil {
			return best, err
		}
		for _, tag := range tags {
			if !validVersion(tag.Name) || (semver.Prerelease(version(tag.Name)) != "" && validVersion(current) && semver.Prerelease(version(current)) == "") || (validVersion(current) && compareVersion(tag.Name, current) <= 0) || (best.Tag != "" && compareVersion(tag.Name, best.Tag) <= 0) {
				continue
			}
			r, err := c.release(ctx, tag.Name)
			if err != nil {
				return Release{}, err
			}
			if r.URL != "" {
				best = r
			}
		}
		if len(tags) < 100 {
			return best, nil
		}
	}
}

func version(v string) string        { return "v" + strings.TrimPrefix(v, "v") }
func validVersion(v string) bool     { return semver.IsValid(version(v)) }
func compareVersion(a, b string) int { return semver.Compare(version(a), version(b)) }
func (c Client) release(ctx context.Context, tag string) (Release, error) {
	resp, err := c.get(ctx, c.API+"/releases/tags/"+url.PathEscape(tag))
	if err != nil {
		// Tags may be visible before the release pipeline publishes assets.
		if err == httpStatus(http.StatusNotFound) {
			return Release{}, nil
		}
		return Release{}, err
	}
	defer resp.Body.Close()
	var release struct {
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
		Draft      bool `json:"draft"`
		Prerelease bool `json:"prerelease"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&release); err != nil {
		return Release{}, err
	}
	if release.Draft {
		return Release{}, nil
	}
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	r := Release{Tag: tag, Name: "pencode-" + tag + "-" + runtime.GOOS + "-" + runtime.GOARCH + ext}
	for _, asset := range release.Assets {
		if asset.Name == r.Name {
			r.URL = asset.URL
		}
		if asset.Name == "SHA256SUMS" {
			r.Checksums = asset.URL
		}
	}
	if r.Checksums == "" {
		r.URL = ""
	}
	return r, nil
}

// Stage checks the archive digest before extracting any files. It never touches the running installation.
func (c Client) Stage(ctx context.Context, root, executable string, r Release) (stage string, err error) {
	stage, err = os.MkdirTemp(filepath.Dir(root), ".pencode-update-*")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(stage)
		}
	}()
	resp, err := c.get(ctx, r.Checksums)
	if err != nil {
		return stage, err
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if err != nil {
		return stage, err
	}
	var expected string
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == r.Name {
			expected = fields[0]
			break
		}
	}
	if digest, e := hex.DecodeString(expected); e != nil || len(digest) != sha256.Size {
		return stage, errors.New("发行包缺少有效 SHA-256 校验值")
	}
	resp, err = c.get(ctx, r.URL)
	if err != nil {
		return stage, err
	}
	defer resp.Body.Close()
	archive := filepath.Join(stage, r.Name)
	file, err := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return stage, err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(file, h), io.LimitReader(resp.Body, (1<<30)+1))
	if err = errors.Join(copyErr, file.Close()); err != nil {
		return stage, err
	}
	if n > 1<<30 || !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), expected) {
		return stage, errors.New("发行包 SHA-256 校验失败")
	}
	if err = extract(archive, stage); err != nil {
		return stage, err
	}
	rel, err := filepath.Rel(root, executable)
	if err != nil || !filepath.IsLocal(rel) {
		return stage, errors.New("程序路径不在安装目录内")
	}
	info, err := os.Stat(filepath.Join(stage, "pencode", rel))
	if err != nil {
		return stage, err
	}
	if !info.Mode().IsRegular() {
		return stage, errors.New("发行包缺少可执行程序")
	}
	data, err = os.ReadFile(filepath.Join(stage, "pencode", VersionFile))
	if err != nil {
		return stage, err
	}
	if strings.TrimSpace(string(data)) != r.Tag {
		return stage, errors.New("发行包版本与 tag 不一致")
	}
	return stage, nil
}
