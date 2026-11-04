package workbench

import (
	"context"
	"encoding/base64"
	"fmt"
	"runtime"
	"time"

	"github.com/diiyw/onix/input"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/internal/clipboard"
	"github.com/pencode-labs/pencode/peb"
)

func readClipboardImage() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return clipboard.ReadImage(ctx)
}

func (s *pencodeExtensionSurface) interceptImagePaste(state *input.State, readImage func() ([]byte, error)) bool {
	if !s.model.commandMatches("editor-paste", state, runtime.GOOS) {
		return false
	}
	world := s.model.fontResources
	control := world.FocusedControl()
	for _, f := range s.focus {
		if !f.pasteImages {
			continue
		}
		if target, ok := world.HTMLControl(s.model.surface, f.id); !ok || target != control {
			continue
		}
		field := world.Mut[engineui.TextField](control)
		if native, ok := world.Get[htmlui.Control](control); field == nil || !ok || native.ReadOnly || native.Disabled {
			return false
		}
		data, err := readImage()
		if err != nil {
			s.model.logOutput("Extension: "+s.owner, fmt.Sprintf("读取剪贴板图片：%v", err))
			return false
		}
		if len(data) == 0 {
			return false
		}
		event := s.commitInput(state, control, field, f.ref)
		s.send(clipboardImageEvent(event, data), "")
		return true
	}
	return false
}

func clipboardImageEvent(event peb.EditorEvent, data []byte) peb.EditorEvent {
	event.Kind, event.InputType = "paste", "image/png"
	if base64.StdEncoding.EncodedLen(len(data))+len("data:image/png;base64,") > peb.MaxDocument {
		event.InputType, event.Data = "error", "图片过大，请粘贴小于 1.5 MiB 的图片"
	} else {
		event.Data = "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
	}
	return event
}
