// Package updater downloads verified native installers into a temporary directory.
package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/mod/semver"
	"golang.org/x/net/html"
)

const VersionFile = "pencode-version"
const releasesPath = "/pencode-labs/pencode/releases"

type httpStatus int

func (s httpStatus) Error() string { return fmt.Sprintf("GitHub HTTP %d", s) }

type Release struct {
	Tag       string
	URL       string
	Checksums string
	Name      string
}

type Client struct {
	HTTP   *http.Client
	GitHub string
}

func NewClient() Client {
	return Client{HTTP: &http.Client{Timeout: 15 * time.Minute}, GitHub: "https://github.com"}
}

func Installation(executable string) (root, version string, err error) {
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return "", "", err
	}
	dir := filepath.Dir(executable)
	versionPath := VersionFile
	if runtime.GOOS == "darwin" {
		versionPath = filepath.Join("Resources", VersionFile)
	}
	for range 5 {
		data, readErr := os.ReadFile(filepath.Join(dir, versionPath))
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

// Check finds the newest eligible version with a published installer and checksums.
func (c Client) Check(ctx context.Context, current string) (Release, error) {
	current = version(current)
	stable := semver.IsValid(current) && semver.Prerelease(current) == ""
	var best Release
	bestVersion := current
	for page := 1; ; page++ {
		tags, more, err := c.releaseTags(ctx, page)
		if err != nil {
			return Release{}, err
		}
		for _, tag := range tags {
			candidate := version(tag)
			if stable && semver.Prerelease(candidate) != "" || semver.Compare(candidate, bestVersion) <= 0 {
				continue
			}
			r, err := c.release(ctx, tag)
			if err != nil {
				return Release{}, err
			}
			if r.URL != "" {
				best = r
				bestVersion = candidate
			}
		}
		if !more {
			return best, nil
		}
	}
}

func version(v string) string {
	if strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

func (c Client) scanReleaseHTML(ctx context.Context, address string, visit func(*html.Tokenizer) bool) error {
	resp, err := c.get(ctx, address)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	const limit = 4 << 20
	reader := &io.LimitedReader{R: resp.Body, N: limit + 1}
	tokens := html.NewTokenizer(reader)
	for {
		switch tokens.Next() {
		case html.StartTagToken, html.SelfClosingTagToken:
			if visit(tokens) {
				return nil
			}
		case html.ErrorToken:
			if reader.N == 0 {
				return errors.New("GitHub 发布页面超过 4 MiB")
			}
			if err := tokens.Err(); err != io.EOF {
				return err
			}
			return nil
		}
	}
}

func (c Client) releaseTags(ctx context.Context, page int) (tags []string, next bool, err error) {
	address := c.GitHub + releasesPath
	if page > 1 {
		address += "?page=" + strconv.Itoa(page)
	}
	nextPath := releasesPath + "?page=" + strconv.Itoa(page+1)
	err = c.scanReleaseHTML(ctx, address, func(tokens *html.Tokenizer) bool {
		name, more := tokens.TagName()
		switch string(name) {
		case "include-fragment":
			for more {
				var key, value []byte
				key, value, more = tokens.TagAttr()
				if string(key) == "src" {
					path := strings.TrimPrefix(string(value), c.GitHub)
					if escaped, ok := strings.CutPrefix(path, releasesPath+"/expanded_assets/"); ok {
						if tag, err := url.PathUnescape(escaped); err == nil && semver.IsValid(version(tag)) {
							tags = append(tags, tag)
						}
					}
					break
				}
			}
		case "a":
			var nextLink, nextPage bool
			for more {
				var key, value []byte
				key, value, more = tokens.TagAttr()
				switch string(key) {
				case "rel":
					nextLink = string(value) == "next"
				case "href":
					nextPage = strings.TrimPrefix(string(value), c.GitHub) == nextPath
				}
			}
			next = next || nextLink && nextPage
		}
		return false
	})
	return tags, next, err
}

func (c Client) release(ctx context.Context, tag string) (Release, error) {
	r := Release{Tag: tag, Name: "pencode-" + tag + "-" + runtime.GOOS + "-" + runtime.GOARCH + installerExtension(runtime.GOOS)}
	escapedTag := url.PathEscape(tag)
	prefix := releasesPath + "/download/" + tag + "/"
	download := c.GitHub + releasesPath + "/download/" + escapedTag + "/"
	installer, checksums := prefix+r.Name, prefix+"SHA256SUMS"
	err := c.scanReleaseHTML(ctx, c.GitHub+releasesPath+"/expanded_assets/"+escapedTag, func(tokens *html.Tokenizer) bool {
		name, more := tokens.TagName()
		if string(name) != "a" {
			return false
		}
		for more {
			var key, value []byte
			key, value, more = tokens.TagAttr()
			if string(key) != "href" {
				continue
			}
			path, err := url.PathUnescape(strings.TrimPrefix(string(value), c.GitHub))
			if err == nil {
				switch path {
				case installer:
					r.URL = download + url.PathEscape(r.Name)
				case checksums:
					r.Checksums = download + "SHA256SUMS"
				}
			}
			break
		}
		return r.URL != "" && r.Checksums != ""
	})
	// A release can disappear between reading the list and fetching its assets.
	if err == httpStatus(http.StatusNotFound) {
		return Release{}, nil
	}
	if err != nil {
		return Release{}, err
	}
	if r.URL == "" || r.Checksums == "" {
		return Release{}, nil
	}
	return r, nil
}

func installerExtension(platform string) string {
	switch platform {
	case "windows":
		return ".exe"
	case "darwin":
		return ".pkg"
	default:
		return ".deb"
	}
}

// Stage checks the installer digest without touching the running installation.
func (c Client) Stage(ctx context.Context, root, executable string, r Release) (stage string, err error) {
	if !filepath.IsLocal(r.Name) || filepath.Base(r.Name) != r.Name || !strings.HasSuffix(r.Name, installerExtension(runtime.GOOS)) {
		return "", errors.New("安装包名称或格式无效")
	}
	rel, err := filepath.Rel(root, executable)
	if err != nil || !filepath.IsLocal(rel) {
		return "", errors.New("程序路径不在安装目录内")
	}
	stage, err = os.MkdirTemp("", ".pencode-update-*")
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
	if err = os.WriteFile(filepath.Join(stage, "installer"), []byte(r.Name), 0o600); err != nil {
		return stage, err
	}
	return stage, nil
}
