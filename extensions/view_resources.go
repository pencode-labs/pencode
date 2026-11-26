package extensions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/onix-engine/onix/html/markup"
	"github.com/onix-engine/onix/html/parse"
	imagespng "github.com/onix-engine/onix/images/png"
	"github.com/onix-engine/onix/images/svg"
	imageswebp "github.com/onix-engine/onix/images/webp"
	"github.com/onix-engine/onix/scene/node"
	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/peb"
)

type viewResource struct {
	data   []byte
	config image.Config
}

// Keep only the most recent prepared view. Published asset maps are immutable.
type preparedView struct {
	assets                                  map[string][]byte
	source, markup, surface, root, document string
	regions                                 []preparedRegion
}

type viewResources struct {
	remote         *client.Extension
	cache          map[string]viewResource
	root, document string
	last           preparedView
	bytes          int
}

func (r *viewResources) prepare(ctx context.Context, view *peb.EditorView, surfaceID string) (assets map[string][]byte, err error) {
	if view == nil {
		return nil, nil
	}
	source := view.HTML
	if source == "" {
		r.last = preparedView{}
		return nil, nil
	}
	if last := &r.last; surfaceID == last.surface && r.root == last.root && r.document == last.document {
		if source == last.source || last.updateRegion(source, r.remote, surfaceID) {
			view.HTML = last.markup
			return last.assets, nil
		}
	}
	r.last = preparedView{}
	var regions []preparedRegion
	var byRef map[string]*preparedRegion
	reusable := true
	defer func() {
		if err == nil && reusable {
			if len(regions) != 0 && view.HTML != source {
				regions = bindPreparedRegions(regions, view.HTML)
			}
			r.last = preparedView{regions: regions, source: source, markup: view.HTML, assets: assets, surface: surfaceID, root: r.root, document: r.document}
		}
	}()
	const maxAssets, maxAssetBytes, maxTotalBytes, maxPixels = 64, 4 << 20, 8 << 20, 4 << 20
	root, err := parse.ParseTree(view.HTML)
	if err != nil {
		return nil, err
	}
	// Independently editable regions must have the same boundaries in the
	// authored tree and the HTML parser's tree (including implied end tags).
	if surfaceID != "" && r.document == "" && strings.Contains(source, "data-pencode-region") && markup.HTMLWithSizeHint(root, len(source)) == source {
		regions, byRef = editableRegions(source)
	}
	paths := make([]string, 0, 8)
	sources := make(map[string]string)
	changed, focusCount := false, 0
	iconBytes, iconPixels := 0, 0
	var networkCtx context.Context
	var cancelNetwork context.CancelFunc
	defer func() {
		if cancelNetwork != nil {
			cancelNetwork()
		}
	}()
	var walk func(*node.Node, *preparedRegion, int) error
	walk = func(n *node.Node, region *preparedRegion, flowDepth int) error {
		if r.document != "" && n.Data == "a" && n.AttrValue("href") != "" {
			if _, ok := n.Attr("data-ref"); !ok {
				n.SetAttr("data-ref", "@peb-link-"+strconv.Itoa(focusCount))
				focusCount++
				changed = true
			}
		}
		if surfaceID != "" && n.Type == node.ElementNode && prepareSurfaceControl(n, r.remote, surfaceID, &focusCount) {
			changed = true
		}
		var entered *preparedRegion
		if flowDepth >= 0 && byRef != nil {
			if n.Type != node.DocumentNode && !flowContainer(n.Data) {
				flowDepth = -1
			} else if _, marked := n.Attr("data-pencode-region"); marked {
				entered = byRef[n.AttrValue("data-ref")]
				if entered != nil {
					region = entered
					region.depth, region.focusStart = flowDepth, focusCount
				}
				flowDepth = -1 // PEB patch boundaries are the outermost regions.
			} else {
				flowDepth++
			}
		}

		if source, ok := n.Attr("src"); ok && n.Type == node.ElementNode && n.Data == "img" {
			prepareImage := func() error {
				// Document views request lazy images from the paint window, after layout.
				if r.document != "" && surfaceID == "" && n.AttrValue("loading") == "lazy" && !strings.HasPrefix(source, "peb:") {
					return nil
				}
				inline := strings.HasPrefix(source, "data:")
				var parsed *url.URL
				var parseErr error
				if !inline {
					parsed, parseErr = url.Parse(source)
				}
				remoteImage := surfaceID != "" && parsed != nil && (parsed.Scheme == "http" || parsed.Scheme == "https")
				svgIcon := surfaceID != "" && strings.HasPrefix(source, "data:image/svg+xml;base64,")
				documentImage := !inline && r.document != "" && (parseErr != nil || parsed.Scheme != "peb" && !validResourcePath(source, true))
				if inline || remoteImage || documentImage {
					changed = true
					var uri string
					var err error
					switch {
					case inline:
						uri, err = r.inlineImage(source)
						if err != nil && !svgIcon {
							return err
						}
					case remoteImage:
						// All network images in an action share a budget so an
						// unreachable icon cannot exhaust the action reply deadline.
						if networkCtx == nil {
							networkCtx, cancelNetwork = context.WithTimeout(ctx, StartupTimeout/2)
						}
						uri, err = r.documentImage(networkCtx, source)
					default:
						uri, err = r.documentImage(ctx, source)
					}
					if _, used := sources[uri]; !used && err == nil && (remoteImage || svgIcon) {
						asset := r.cache[uri]
						pixels := asset.config.Width * asset.config.Height
						if len(paths) == maxAssets || iconBytes+len(asset.data) > maxTotalBytes || iconPixels+pixels > maxPixels {
							err = fmt.Errorf("菜单图标总量超过限制")
						} else {
							iconBytes += len(asset.data)
							iconPixels += pixels
						}
					}
					if err != nil {
						// Keep the control usable and retry missing images on the next update.
						reusable = false
						n.RemoveAttr("src")
						n.SetAttr("title", err.Error())
						return nil
					}
					n.SetAttr("src", uri)
					if _, ok := sources[uri]; !ok {
						if len(paths) == maxAssets {
							return fmt.Errorf("编辑器图片资源超过 %d 项", maxAssets)
						}
						sources[uri] = uri
						paths = append(paths, uri)
					}
					return nil
				}
				if parseErr != nil {
					return parseErr
				}
				name := parsed.Path
				if parsed.Scheme == "peb" && parsed.Host == r.remote.Info.ID && (strings.HasPrefix(parsed.Path, "/inline/") || strings.HasPrefix(parsed.Path, "/document/")) {
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
					return nil
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
				n.SetAttr("src", uri)
				if _, ok := sources[name]; !ok {
					if len(paths) == maxAssets {
						return fmt.Errorf("编辑器图片资源超过 %d 项", maxAssets)
					}
					sources[name] = uri
					paths = append(paths, name)
				}
				return nil
			}
			if err := prepareImage(); err != nil {
				return err
			}
			if region != nil {
				region.images = append(region.images, regionImage{strings.Clone(source), strings.Clone(n.AttrValue("src"))})
			}
		}
		for _, child := range n.Children {
			if err := walk(child, region, flowDepth); err != nil {
				return err
			}
		}
		if entered != nil {
			entered.focusEnd = focusCount
		}
		return nil
	}
	if err := walk(root, nil, 0); err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		if changed {
			view.HTML = markup.HTMLWithSizeHint(root, len(source))
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
	assets = make(map[string][]byte, len(paths))
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
	view.HTML = markup.HTMLWithSizeHint(root, len(source))
	return assets, nil
}

func prepareSurfaceControl(n *node.Node, remote *client.Extension, surfaceID string, focusCount *int) bool {
	_, tab := n.Attr("tabindex")
	_, href := n.Attr("href")
	edit, editable := n.Attr("contenteditable")
	if !(n.Data == "form" || n.Data == "button" || n.Data == "input" || n.Data == "select" || n.Data == "textarea" || n.Data == "a" && href || tab || editable && edit != "false") {
		return false
	}
	ref := n.AttrValue("data-ref")
	generated := ref == ""
	if generated {
		ref = "@peb-focus-" + strconv.Itoa(*focusCount)
		n.SetAttr("data-ref", ref)
	}
	*focusCount++
	if n.AttrValue("id") != "" {
		return generated
	}
	n.SetAttr("id", "pencode-extension-ui/"+remote.Info.ID+"/"+surfaceID+"/control-"+ref)
	return true
}

// Inline images become validated, memory-backed assets just like bundled
// images. This also lets remote extensions show clipboard previews without files.
func (r *viewResources) inlineImage(source string) (string, error) {
	header, encoded, ok := strings.Cut(source, ",")
	var ext string
	switch header {
	case "data:image/png;base64":
		ext = ".png"
	case "data:image/jpeg;base64":
		ext = ".jpg"
	case "data:image/svg+xml;base64":
		ext = ".svg"
	default:
		return "", fmt.Errorf("仅支持 PNG、JPEG 或 SVG 内嵌图片")
	}
	if !ok || len(source) > peb.MaxDocument {
		return "", fmt.Errorf("内嵌图片超过大小限制")
	}
	uri := fmt.Sprintf("peb://%s/inline/%x%s", r.remote.Info.ID, sha256.Sum256([]byte(source)), ext)
	if _, ok := r.cache[uri]; ok {
		return uri, nil
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	config, err := imageConfig(ext, data)
	if err != nil {
		return "", err
	}
	if config.Width < 1 || config.Height < 1 || config.Width > 2048 || config.Height > 2048 || r.bytes+len(data) > maxResourceBytes {
		return "", fmt.Errorf("内嵌图片超过大小或尺寸限制")
	}
	r.cache[uri] = viewResource{data, config}
	r.bytes += len(data)
	return uri, nil
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

// DocumentImage holds validated bytes and intrinsic dimensions for a single paint request.
type DocumentImage struct {
	URI           string
	Data          []byte
	Width, Height int
}

func (e *Editor) ReadDocumentImage(ctx context.Context, root, document, source string) (DocumentImage, error) {
	// Each lazy read has its own budget. The view owns successful results.
	r := viewResources{remote: e.remote, root: root, document: document, cache: make(map[string]viewResource, 1)}
	var uri string
	var err error
	if strings.HasPrefix(source, "data:") {
		uri, err = r.inlineImage(source)
	} else {
		uri, err = r.documentImage(ctx, source)
	}
	if err != nil {
		return DocumentImage{}, err
	}
	asset := r.cache[uri]
	return DocumentImage{uri, asset.data, asset.config.Width, asset.config.Height}, nil
}
