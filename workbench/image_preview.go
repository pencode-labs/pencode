package workbench

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"html"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"path"
	"strings"

	onixsvg "github.com/onix-engine/onix/images/svg"
	"github.com/onix-engine/onix/images/webp"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"
)

var pencodeImageEditor = extensions.Editor{Action: peb.Action{ID: "image", Text: "图片预览", Default: true}}

func imageFile(name string) bool {
	ext := path.Ext(name)
	for _, supported := range [...]string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".tif", ".tiff", ".svg"} {
		if strings.EqualFold(ext, supported) {
			return true
		}
	}
	return false
}

type pencodeImageResult struct {
	source        string
	data          []byte
	width, height int
}

func (document *pencodeCustomDocument) loadImage() {
	model, file := document.model, document.file
	root := model.projectPath
	if document.project != nil {
		root = document.project.Path
	}
	document.content = file.content()
	job := &fileIOJob{kind: fileImagePreview, project: document.project, file: file, imageDocument: document, root: root, relative: file.Path}
	if strings.EqualFold(path.Ext(file.Name), ".svg") && file.text() {
		job.content, job.baseline, job.mode = document.content, file.snapshot(), file.openMode()
	}
	if err := model.submitFileIO(job); err != nil {
		document.message = model.tr("图片预览失败：") + err.Error()
		return
	}
	document.inflight = true
	document.message = model.tr("正在加载图片…")
}

func executeImagePreview(job *fileIOJob) (*pencodeImageResult, error) {
	var data []byte
	var err error
	if job.mode != 0 {
		data = []byte(job.content)
	} else {
		data, err = job.backend.ReadFile(job.ctx, job.root, job.relative)
	}
	if err != nil {
		return nil, err
	}
	return prepareImagePreview(data, strings.ToLower(path.Ext(job.relative)))
}

// Validate the pixel budget before decoding. Formats outside the renderer's
// static decoder set are normalized on the file worker, never on an idle frame.
func prepareImagePreview(data []byte, ext string) (*pencodeImageResult, error) {
	reader := bytes.NewReader(data)
	var config image.Config
	var err error
	switch ext {
	case ".svg":
		config, err = onixsvg.DecodeConfig(reader)
	case ".png":
		config, err = png.DecodeConfig(reader)
	case ".jpg", ".jpeg":
		config, err = jpeg.DecodeConfig(reader)
	case ".gif":
		config, err = gif.DecodeConfig(reader)
	case ".webp":
		config, err = webp.DecodeConfig(reader)
	case ".bmp":
		config, err = bmp.DecodeConfig(reader)
	case ".tif", ".tiff":
		config, err = tiff.DecodeConfig(reader)
	default:
		return nil, fmt.Errorf("不支持的图片格式 %s", ext)
	}
	if err != nil {
		return nil, err
	}
	const maxPixels = 16 << 20
	if config.Width <= 0 || config.Height <= 0 || config.Width > maxPixels/config.Height {
		return nil, fmt.Errorf("图片尺寸超出预览范围（最多 %d 像素）", maxPixels)
	}
	var decoded image.Image
	reader.Reset(data)
	switch ext {
	case ".png":
		decoded, err = png.Decode(reader)
	case ".jpg", ".jpeg":
		decoded, err = jpeg.Decode(reader)
	case ".webp":
		decoded, err = webp.Decode(reader)
	case ".gif":
		decoded, err = gif.Decode(reader)
	case ".bmp":
		decoded, err = bmp.Decode(reader)
	case ".tif", ".tiff":
		decoded, err = tiff.Decode(reader)
	}
	if err != nil {
		return nil, err
	}
	if ext == ".gif" || ext == ".bmp" || ext == ".tif" || ext == ".tiff" {
		var buffer bytes.Buffer
		if err := png.Encode(&buffer, decoded); err != nil {
			return nil, err
		}
		data, ext = buffer.Bytes(), ".png"
	}
	return &pencodeImageResult{source: fmt.Sprintf("peb://image/%x%s", sha256.Sum256(data), ext), data: data, width: config.Width, height: config.Height}, nil
}

const pencodeImageCSS = `
.image-preview { position:absolute; inset:0; display:flex; align-items:center; justify-content:center; padding:48px 24px; }
.image-preview img { max-width:100%; max-height:100%; object-fit:contain; }
.image-info { position:absolute; bottom:12px; left:16px; right:16px; margin:0; text-align:center; color:var(--vscode-descriptionForeground); }
`

func (model *workspaceModel) acceptImagePreview(result *fileIOResult) {
	job := result.job
	document := job.imageDocument
	if model.customDocuments[job.file] != document || !model.fileLive(job.project, job.relative, job.file) || model.fileEditor(job.file) != &pencodeImageEditor {
		return
	}
	document.inflight = false
	if job.mode != 0 && !job.file.snapshot().Equal(job.baseline) {
		return
	}
	if result.err == nil {
		result.err = job.ctx.Err()
	}
	if result.err != nil {
		document.message = model.tr("图片预览失败：") + result.err.Error()
		document.definition = nil
		document.view = nil
		document.images = model.replaceExtensionImages(document.images, nil)
		return
	}
	preview := result.image
	view := &peb.EditorView{HTML: fmt.Sprintf(`<div id="pencode-extension-image-container" class="image-preview"><img id="pencode-extension-image" src="%s" alt="%s" width="%d" height="%d"></div><p class="image-info">%d × %d</p>`, preview.source, html.EscapeString(job.file.Name), preview.width, preview.height, preview.width, preview.height), CSS: pencodeImageCSS}
	if err := document.setView(view); err != nil {
		document.message = model.tr("图片预览失败：") + err.Error()
		return
	}
	document.images = model.replaceExtensionImages(document.images, map[string][]byte{preview.source: preview.data})
	document.message = ""
}
