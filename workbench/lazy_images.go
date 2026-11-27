package workbench

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/onix-engine/onix/ecs"
	"github.com/onix-engine/onix/scene/node"
	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/extensions"
)

const lazyImagePrefix = "peb-lazy:"
const concurrentImageReads = 4

type lazyDocumentImages struct {
	ctx          context.Context
	cancel       context.CancelFunc
	bySource     map[string]*lazyDocumentImage
	placeholders map[string]*lazyDocumentImage
	assets       map[string][]byte
	results      chan lazyImageResult
	queue        []*lazyDocumentImage
	resolved     []*lazyDocumentImage
	applied      int
	running      int
	component    ecs.Entity
}

type lazyDocumentImage struct {
	source, placeholder, uri, width, height, failure string
	bindings                                         []lazyImageBinding
	aspect                                           float64
	requested                                        bool
}

type lazyImageBinding struct {
	ref, width, height string
}

type lazyImageResult struct {
	err   error
	image *lazyDocumentImage
	asset extensions.DocumentImage
}

func (d *pencodeCustomDocument) bindLazyImage(next **lazyDocumentImages, n *node.Node) {
	source := n.AttrValue("src")
	if source == "" || strings.HasPrefix(source, "peb:") {
		return
	}
	if *next == nil {
		*next = &lazyDocumentImages{bySource: make(map[string]*lazyDocumentImage), placeholders: make(map[string]*lazyDocumentImage), assets: make(map[string][]byte)}
	}
	l := *next
	img := l.bySource[source]
	if img == nil {
		img = &lazyDocumentImage{source: source}
		img.placeholder = fmt.Sprintf("%s%p", lazyImagePrefix, img)
		if old := d.lazy; old != nil {
			if cached := old.bySource[source]; cached != nil && cached.uri != "" {
				img.uri, img.width, img.height, img.aspect = cached.uri, cached.width, cached.height, cached.aspect
				l.assets[img.uri] = old.assets[img.uri]
				l.resolved = append(l.resolved, img)
			}
		}
		l.bySource[source], l.placeholders[img.placeholder] = img, img
	}
	ref := n.AttrValue("data-ref")
	if ref == "" {
		ref = img.placeholder + "/" + strconv.Itoa(len(img.bindings))
		n.SetAttr("data-ref", ref)
	}
	b := lazyImageBinding{ref: ref, width: n.AttrValue("width"), height: n.AttrValue("height")}
	// Give unsized inline images a paintable box before intrinsic dimensions arrive.
	n.SetAttr("src", img.placeholder)
	if b.width == "" {
		n.SetAttr("width", "300")
	}
	if b.height == "" {
		n.SetAttr("height", "150")
	}
	img.bindings = append(img.bindings, b)
}

func (d *pencodeCustomDocument) replaceLazyImages(next *lazyDocumentImages) {
	var before, after map[string][]byte
	if old := d.lazy; old != nil {
		if old.cancel != nil {
			old.cancel()
		}
		before = old.assets
	}
	if next != nil {
		after = next.assets
	}
	if len(before) != 0 || len(after) != 0 {
		d.model.replaceExtensionImages(before, after)
	}
	d.lazy = next
}

// A mounted preview keeps its event bindings and patches only newly resolved images.
// Remounts (including tab switches) replay all results into the new component.
func (l *lazyDocumentImages) render(target *htmlui.View, component ecs.Entity, static bool) (patched bool) {
	images := l.resolved
	if static && l.component == component {
		target.PatchBindings()
		images = images[l.applied:]
		patched = true
	}
	for _, img := range images {
		for _, binding := range img.bindings {
			if img.failure != "" {
				target.RemoveAttr(binding.ref, "src")
				target.Attr(binding.ref, "title", img.failure)
				continue
			}
			target.Attr(binding.ref, "src", img.uri)
			if binding.width == "" {
				target.Attr(binding.ref, "width", lazyImageDimension(binding.height, img.aspect, img.width))
			}
			if binding.height == "" {
				target.Attr(binding.ref, "height", lazyImageDimension(binding.width, 1/img.aspect, img.height))
			}
		}
	}
	l.component, l.applied = component, len(l.resolved)
	return patched
}

func lazyImageDimension(explicit string, ratio float64, fallback string) string {
	if explicit != "" {
		if size, err := strconv.ParseFloat(strings.TrimSpace(explicit), 64); err == nil && size > 0 {
			return strconv.FormatFloat(size*ratio, 'g', -1, 64)
		}
	}
	return fallback
}

// The HTML renderer resolves only images inside its scrollable paint window.
func (d *pencodeCustomDocument) requestLazyImage(placeholder string) {
	l := d.lazy
	img := l.placeholders[placeholder]
	if img == nil || img.requested || img.uri != "" {
		return
	}
	img.requested = true
	if l.results == nil {
		l.ctx, l.cancel = context.WithCancel(context.Background())
		l.results = make(chan lazyImageResult, concurrentImageReads)
	}
	l.queue = append(l.queue, img)
	d.startLazyReads()
}

func (d *pencodeCustomDocument) startLazyReads() {
	l := d.lazy
	for l.running < concurrentImageReads && len(l.queue) > 0 {
		img := l.queue[0]
		l.queue[0] = nil
		l.queue = l.queue[1:]
		l.running++
		root := d.model.projectPath
		if d.project != nil {
			root = d.project.Path
		}
		filename := path.Join(strings.ReplaceAll(root, "\\", "/"), d.file.Path)
		editor, wake := d.provider, d.model.backgroundWake()
		go func() {
			ctx, cancel := context.WithTimeout(l.ctx, extensions.StartupTimeout)
			asset, err := editor.ReadDocumentImage(ctx, root, filename, img.source)
			cancel()
			select {
			case <-l.ctx.Done():
			case l.results <- lazyImageResult{err, img, asset}:
				if wake != nil {
					wake()
				}
			}
		}()
	}
}

func (d *pencodeCustomDocument) pollLazyImages() {
	l := d.lazy
	updated := false
	for l.running > 0 {
		select {
		case result := <-l.results:
			l.running--
			img, asset := result.image, result.asset
			if result.err != nil {
				img.failure = result.err.Error()
			} else {
				if _, owned := l.assets[asset.URI]; !owned {
					d.model.replaceExtensionImages(nil, map[string][]byte{asset.URI: asset.Data})
					l.assets[asset.URI] = asset.Data
				}
				img.uri, img.width, img.height = asset.URI, strconv.Itoa(asset.Width), strconv.Itoa(asset.Height)
				img.aspect = float64(asset.Width) / float64(asset.Height)
			}
			l.resolved = append(l.resolved, img)
			updated = true
			d.startLazyReads()
		default:
			goto done
		}
	}
done:
	if updated && d.model.fontResources != nil {
		d.model.fontResources.UpdateState[*pencodeCustomDocument, struct{}](l.component, func(*struct{}) {})
	}
}
