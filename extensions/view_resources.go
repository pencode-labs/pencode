package extensions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	imagespng "github.com/diiyw/onix/images/png"
	"github.com/diiyw/onix/images/svg"
	imageswebp "github.com/diiyw/onix/images/webp"
	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/peb"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

type viewResource struct {
	data   []byte
	config image.Config
}
type viewResources struct {
	remote         *client.Extension
	cache          map[string]viewResource
	bytes          int
	root, document string
}

func (r *viewResources) prepare(ctx context.Context, view *peb.EditorView, surfaceID string) (map[string][]byte, error) {
	if view == nil {
		return nil, nil
	}
	const maxAssets, maxAssetBytes, maxTotalBytes, maxPixels = 64, 4 << 20, 8 << 20, 4 << 20
	nodes, err := html.ParseFragment(strings.NewReader(view.HTML), &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div})
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, 8)
	sources := make(map[string]string)
	changed, focusCount := false, 0
	var walk func(*html.Node) error
	walk = func(node *html.Node) error {
		if r.document != "" && node.Type == html.ElementNode && node.Data == "a" {
			for _, a := range node.Attr {
				if a.Key == "href" && a.Val != "" {
					hasRef := false
					for _, attr := range node.Attr {
						hasRef = hasRef || attr.Key == "data-ref"
					}
					if !hasRef {
						node.Attr = append(node.Attr, html.Attribute{Key: "data-ref", Val: "@peb-link-" + strconv.Itoa(focusCount)})
						focusCount++
						changed = true
					}
					break
				}
			}
		}
		if surfaceID != "" && node.Type == html.ElementNode {
			ref, id, tab, href, editable := false, false, false, false, false
			refIndex, idIndex := -1, -1
			for i, a := range node.Attr {
				switch a.Key {
				case "data-ref":
					ref = a.Val != ""
					refIndex = i
				case "id":
					id = a.Val != ""
					idIndex = i
				case "tabindex":
					tab = true
				case "href":
					href = true
				case "contenteditable":
					editable = a.Val != "false"
				}
			}
			if node.Data == "form" || node.Data == "button" || node.Data == "input" || node.Data == "select" || node.Data == "textarea" || node.Data == "a" && href || tab || editable {
				suffix := strconv.Itoa(focusCount)
				focusCount++
				if !ref {
					value := "@peb-focus-" + suffix
					if refIndex >= 0 {
						node.Attr[refIndex].Val = value
					} else {
						node.Attr = append(node.Attr, html.Attribute{Key: "data-ref", Val: value})
					}
					changed = true
				}
				if !id {
					value := "pencode-extension-ui/" + r.remote.Info.ID + "/" + surfaceID + "/control/" + suffix
					if idIndex >= 0 {
						node.Attr[idIndex].Val = value
					} else {
						node.Attr = append(node.Attr, html.Attribute{Key: "id", Val: value})
					}
					changed = true
				}
			}
		}
		if node.Type == html.ElementNode && node.Data == "img" {
			for i := range node.Attr {
				if node.Attr[i].Key != "src" {
					continue
				}
				source := node.Attr[i].Val
				parsed, parseErr := url.Parse(source)
				if r.document != "" && (parseErr != nil || parsed.Scheme != "peb" && !validResourcePath(source, true)) {
					changed = true
					uri, err := r.documentImage(ctx, source)
					if err != nil {
						// A missing image must not hide the rest of the document.
						node.Attr = append(node.Attr[:i], node.Attr[i+1:]...)
						node.Attr = append(node.Attr, html.Attribute{Key: "title", Val: err.Error()})
						break
					}
					node.Attr[i].Val = uri
					if _, ok := sources[uri]; !ok {
						if len(paths) == maxAssets {
							return fmt.Errorf("编辑器图片资源超过 %d 项", maxAssets)
						}
						sources[uri] = uri
						paths = append(paths, uri)
					}
					break
				}
				if parseErr != nil {
					return parseErr
				}
				name := parsed.Path
				if r.document != "" && parsed.Scheme == "peb" && parsed.Host == r.remote.Info.ID && strings.HasPrefix(parsed.Path, "/document/") {
					if _, ok := r.cache[source]; !ok {
						return fmt.Errorf("未知的文档图片 %q", source)
					}
					if _, ok := sources[source]; !ok {
						if len(paths) == maxAssets {
							return fmt.Errorf("编辑器图片资源超过 %d 项", maxAssets)
						}
						sources[source] = source
						paths = append(paths, source)
					}
					break
				}
				if parsed.Scheme == "peb" && parsed.Host == r.remote.Info.ID {
					name = strings.TrimPrefix(name, "/")
				} else if parsed.Scheme != "" || parsed.Host != "" {
					return fmt.Errorf("编辑器图片必须引用自身扩展资源")
				}
				if parsed.RawQuery != "" || parsed.Fragment != "" || !validResourcePath(name, true) {
					return fmt.Errorf("无效的编辑器图片资源 %q", source)
				}
				if path.Ext(name) != ".svg" && path.Ext(name) != ".png" && path.Ext(name) != ".jpg" && path.Ext(name) != ".jpeg" && path.Ext(name) != ".webp" {
					return fmt.Errorf("不支持的编辑器图片格式 %q", name)
				}
				uri := "peb://" + r.remote.Info.ID + "/" + name
				node.Attr[i].Val = uri
				if _, ok := sources[name]; !ok {
					if len(paths) == maxAssets {
						return fmt.Errorf("编辑器图片资源超过 %d 项", maxAssets)
					}
					sources[name] = uri
					paths = append(paths, name)
				}
				break
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	for _, node := range nodes {
		if err := walk(node); err != nil {
			return nil, err
		}
	}
	if len(paths) == 0 {
		if changed {
			return nil, renderViewNodes(view, nodes)
		}
		return nil, nil
	}
	missing := make([]string, 0, len(paths))
	for _, name := range paths {
		if _, ok := r.cache[name]; !ok {
			missing = append(missing, name)
		}
	}
	resources, err := r.remote.ReadResources(ctx, missing, maxAssetBytes)
	if err != nil {
		return nil, err
	}
	for name, data := range resources {
		if len(data) == 0 || len(data) > maxAssetBytes || r.bytes+len(data) > maxResourceBytes {
			return nil, fmt.Errorf("编辑器图片资源超过大小限制")
		}
		config, err := imageConfig(name, data)
		if err != nil {
			return nil, fmt.Errorf("编辑器图片 %s: %w", name, err)
		}
		if config.Width < 1 || config.Height < 1 || config.Width > 2048 || config.Height > 2048 {
			return nil, fmt.Errorf("编辑器图片尺寸超过限制")
		}
		r.cache[name] = viewResource{data, config}
		r.bytes += len(data)
	}
	assets := make(map[string][]byte, len(paths))
	totalBytes, totalPixels := 0, int64(0)
	for _, name := range paths {
		asset := r.cache[name]
		totalBytes += len(asset.data)
		totalPixels += int64(asset.config.Width) * int64(asset.config.Height)
		if totalBytes > maxTotalBytes || totalPixels > maxPixels {
			return nil, fmt.Errorf("编辑器图片总量超过限制")
		}
		assets[sources[name]] = asset.data
	}
	return assets, renderViewNodes(view, nodes)
}

// Document images are loaded on the session worker, then shared with the GUI as
// validated, memory-backed peb assets. Repeated previews reuse the session cache.
func (r *viewResources) documentImage(ctx context.Context, source string) (string, error) {
	const limit = 4 << 20
	u, err := url.Parse(source)
	if err != nil {
		return "", err
	}
	if u.Scheme == "" && u.Host != "" {
		u.Scheme = "https"
	}
	if u.Scheme == "" {
		base := url.URL{Scheme: "file", Path: "/" + strings.TrimPrefix(strings.ReplaceAll(r.document, "\\", "/"), "/")}
		u = base.ResolveReference(u)
	}
	u.Fragment = ""
	key := u.String()
	ext := strings.ToLower(path.Ext(u.Path))
	// The key retains an extension so the GUI can select its image decoder.
	prefix := fmt.Sprintf("peb://%s/document/%x", r.remote.Info.ID, sha256.Sum256([]byte(key)))
	for _, suffix := range [...]string{".svg", ".png", ".jpg", ".jpeg", ".webp"} {
		if _, ok := r.cache[prefix+suffix]; ok {
			return prefix + suffix, nil
		}
	}
	var data []byte
	switch u.Scheme {
	case "file":
		if u.Host != "" || u.RawQuery != "" {
			return "", fmt.Errorf("无效的文档图片 %q", source)
		}
		root := strings.TrimSuffix("/"+strings.TrimPrefix(strings.ReplaceAll(r.root, "\\", "/"), "/"), "/")
		name, ok := strings.CutPrefix(path.Clean(u.Path), root+"/")
		if !ok || !validResourcePath(name, false) {
			return "", fmt.Errorf("图片不在项目目录内：%s", source)
		}
		data, err = r.remote.ReadWorkspaceFile(ctx, r.root, name, limit)
	case "http", "https":
		var request *http.Request
		request, err = http.NewRequestWithContext(ctx, http.MethodGet, key, nil)
		if err != nil {
			return "", err
		}
		var response *http.Response
		response, err = http.DefaultClient.Do(request)
		if err != nil {
			return "", err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return "", fmt.Errorf("加载图片：HTTP %d", response.StatusCode)
		}
		data, err = io.ReadAll(io.LimitReader(response.Body, limit+1))
	default:
		return "", fmt.Errorf("不支持的文档图片地址 %q", source)
	}
	if err != nil {
		return "", err
	}
	if len(data) == 0 || len(data) > limit || r.bytes+len(data) > maxResourceBytes {
		return "", fmt.Errorf("文档图片超过大小限制")
	}
	if ext != ".svg" && ext != ".png" && ext != ".jpg" && ext != ".jpeg" && ext != ".webp" {
		switch http.DetectContentType(data) {
		case "image/png":
			ext = ".png"
		case "image/jpeg":
			ext = ".jpg"
		case "image/webp":
			ext = ".webp"
		default:
			if bytes.Contains(data[:min(len(data), 512)], []byte("<svg")) {
				ext = ".svg"
			}
		}
	}
	config, err := imageConfig("image"+ext, data)
	if err != nil {
		return "", err
	}
	if config.Width < 1 || config.Height < 1 || config.Width > 2048 || config.Height > 2048 {
		return "", fmt.Errorf("文档图片尺寸超过限制")
	}
	uri := prefix + ext
	r.cache[uri] = viewResource{data, config}
	r.bytes += len(data)
	return uri, nil
}

func renderViewNodes(view *peb.EditorView, nodes []*html.Node) error {
	var markup strings.Builder
	for _, node := range nodes {
		if err := html.Render(&markup, node); err != nil {
			return err
		}
	}
	view.HTML = markup.String()
	return nil
}

func imageConfig(name string, data []byte) (image.Config, error) {
	switch strings.ToLower(path.Ext(name)) {
	case ".svg":
		return svg.DecodeConfig(bytes.NewReader(data))
	case ".png":
		return imagespng.DecodeConfig(bytes.NewReader(data))
	case ".jpg", ".jpeg":
		return jpeg.DecodeConfig(bytes.NewReader(data))
	case ".webp":
		return imageswebp.DecodeConfig(bytes.NewReader(data))
	default:
		return image.Config{}, fmt.Errorf("unsupported image format")
	}
}
