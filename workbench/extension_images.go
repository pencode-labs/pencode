package workbench

// Views own their immutable asset maps. Only the last owner releases the shared
// bytes; historical, unreferenced previews have no cold cache.
type pencodeExtensionImage struct {
	data       []byte
	references int
}

func (model *workspaceModel) replaceExtensionImages(previous, next map[string][]byte) map[string][]byte {
	if len(previous) == 0 && len(next) == 0 {
		return nil
	}
	if model.extensionImages == nil && len(next) != 0 {
		model.extensionImages = make(map[string]pencodeExtensionImage, len(next))
	}
	for source, data := range next {
		image := model.extensionImages[source]
		if _, retained := previous[source]; !retained {
			image.references++
		}
		image.data = data
		model.extensionImages[source] = image
	}
	for source := range previous {
		if _, retained := next[source]; retained {
			continue
		}
		image := model.extensionImages[source]
		if image.references <= 1 {
			delete(model.extensionImages, source)
		} else {
			image.references--
			model.extensionImages[source] = image
		}
	}
	model.extensionImageRevision++
	return next
}
