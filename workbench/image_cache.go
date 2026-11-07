package workbench

import (
	"math"
	"strings"

	"github.com/onix-engine/onix/graphics"
	"github.com/onix-engine/onix/scene"
)

const (
	pencodeImageCacheBytes   = 8 << 20
	pencodeImageCacheEntries = 256
)

type pencodeCachedImage struct {
	texture             *graphics.Texture
	previous, next      *pencodeCachedImage
	theme               *pencodeTheme
	source              string
	bytes               int
	live, icon, preview bool
}

func (p *pencodeIconProvider) iconScale(intrinsicWidth, intrinsicHeight int, width, height float32) int {
	ratio := float64(1)
	if p.model != nil {
		if p.model.app != nil && p.model.app.Context != nil {
			ratio = p.model.app.Context.DevicePixelRatio()
		}
		if p.model.settings.Zoom > 0 {
			ratio *= float64(p.model.settings.Zoom) / 100
		}
	}
	if width <= 0 {
		width = float32(intrinsicWidth)
	}
	if height <= 0 {
		height = float32(intrinsicHeight)
	}
	return max(1, min(4, int(math.Ceil(max(float64(width)/float64(max(1, intrinsicWidth)), float64(height)/float64(max(1, intrinsicHeight)))*ratio))))
}
func (p *pencodeIconProvider) unlink(image *pencodeCachedImage) {
	if image.previous != nil {
		image.previous.next = image.next
	} else {
		p.oldest = image.next
	}
	if image.next != nil {
		image.next.previous = image.previous
	} else {
		p.newest = image.previous
	}
}
func (p *pencodeIconProvider) touch(image *pencodeCachedImage) {
	p.touched = true
	if p.newest == image {
		return
	}
	p.unlink(image)
	image.previous, image.next = p.newest, nil
	if p.newest != nil {
		p.newest.next = image
	} else {
		p.oldest = image
	}
	p.newest = image
}
func (p *pencodeIconProvider) cacheImage(source string, texture *graphics.Texture, icon, preview bool) {
	var old *graphics.Texture
	if existing := p.textures[source]; existing != nil {
		old = existing.texture
		p.removeImage(existing)
	}
	image := &pencodeCachedImage{source: source, texture: texture, icon: icon, preview: preview, previous: p.newest}
	if p.model != nil {
		image.theme = p.model.workbenchTheme
	}
	if texture != nil {
		for width, height, level := texture.Width, texture.Height, 0; level < texture.MipLevels(); level++ {
			image.bytes += width * height * 4
			width = max(1, width/2)
			height = max(1, height/2)
		}
		if p.images == nil {
			p.images = make(map[*graphics.Texture]*pencodeCachedImage)
		}
		p.images[texture] = image
	}
	if p.newest != nil {
		p.newest.next = image
	} else {
		p.oldest = image
	}
	p.newest = image
	p.textures[source] = image
	p.bytes += image.bytes
	p.touched = true
	// A larger icon can be shared with existing smaller instances. Retarget
	// their ECS dependencies so no live sprite retains the retired texture.
	if old != nil && p.content != nil {
		for entity, access := range p.content.Query[scene.Sprite2D]() {
			if access.Ref().Texture == old {
				p.content.Mut[scene.Sprite2D](entity).Texture = texture
			}
		}
		for entity, access := range p.content.Query[scene.SpriteLayers2D]() {
			for i, layer := range access.Ref().Layers {
				if layer.Sprite.Texture == old {
					p.content.Mut[scene.SpriteLayers2D](entity).Layers[i].Sprite.Texture = texture
				}
			}
		}
	}
}
func (p *pencodeIconProvider) removeImage(image *pencodeCachedImage) {
	p.unlink(image)
	delete(p.textures, image.source)
	p.bytes -= image.bytes
	if image.texture != nil {
		delete(p.images, image.texture)
		_ = image.texture.Dispose()
	}
}

// Called after reconciliation, when every retained sprite's texture is known.
// Live textures are pinned; only cached, unreferenced images can be evicted.
func (p *pencodeIconProvider) trimImages() {
	if p == nil || p.model == nil {
		return
	}
	theme := p.model.workbenchTheme
	if !p.touched && p.imageRevision == p.model.extensionImageRevision && p.theme == theme && p.bytes <= pencodeImageCacheBytes && len(p.textures) <= pencodeImageCacheEntries {
		return
	}
	p.theme = theme
	p.imageRevision = p.model.extensionImageRevision
	p.touched = false
	for image := p.oldest; image != nil; image = image.next {
		image.live = false
	}
	if p.content != nil {
		for _, access := range p.content.Query[scene.Sprite2D]() {
			if image := p.images[access.Ref().Texture]; image != nil {
				image.live = true
			}
		}
		for _, access := range p.content.Query[scene.SpriteLayers2D]() {
			for _, layer := range access.Ref().Layers {
				if image := p.images[layer.Sprite.Texture]; image != nil {
					image.live = true
				}
			}
		}
	}
	for image := p.oldest; image != nil; {
		next := image.next
		asset, _, _ := strings.Cut(image.source, "?")
		orphan := image.preview && p.model.extensionImages[asset].references == 0
		p.touched = p.touched || orphan && image.live
		if !image.live && (orphan || image.theme != theme || p.bytes > pencodeImageCacheBytes || len(p.textures) > pencodeImageCacheEntries) {
			p.removeImage(image)
		}
		image = next
	}
}
