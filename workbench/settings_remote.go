package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"time"

	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/configuration"
)

func (model *workspaceModel) applyConfiguration(snapshot configuration.Snapshot, remote bool) {
	model.settings = parsePencodeSettings(snapshot.Files["settings.json"])
	for _, group := range model.extensionSettings {
		model.rebuildExtensionSettings(group)
	}
	model.settingsPath = path.Join(snapshot.Directory, "settings.json")
	model.settingsRemote, model.settingsFeedback, model.capturingShortcut = remote, "", false
	model.themes, model.workbenchTheme, model.syntaxTheme, model.syntaxDefinitions = nil, nil, nil, nil
	if err := model.loadSyntaxResources(snapshot); err != nil {
		model.settingsFeedback = fmt.Sprintf("加载配置失败：%v", err)
	}
	// System faces render on the GUI host. Extension face IDs remain selected
	// until the new host publishes its registrations.
	model.loadFont(model.settings.CodeFont)
	model.loadFont(model.settings.UIFont)
	model.syncFontOptions()
	model.refreshSettingsLabels()
	model.applySettingsToView()
	model.resetHighlight()
}

func (model *workspaceModel) applySettingsToView() {
	model.applyUIFont()
	if world := model.fontResources; world != nil {
		if surface := world.Mut[htmlui.Surface](model.surface); surface != nil {
			surface.FontSize = float64(model.settings.UIFontSize)
		}
		if textarea := world.Mut[engineui.Textarea](model.editor.control); textarea != nil {
			textarea.WrapWidthPercent = float32(model.settings.EditorWidth)
		}
		model.syncZoom(world)
	}
}

// One worker per backend serializes saves. Only the newest waiting snapshot is
// retained; a running save completes before the next begins. Switching devices
// cannot redirect an already queued write to a different server.
type pencodeSettingsWriter struct {
	requests chan pencodeSettingsWrite
	results  chan pencodeSettingsWriteResult
	done     chan struct{}
	revision uint64 // Foreground-owned; workers only echo a request's revision.
}

type pencodeSettingsWrite struct {
	data     []byte
	revision uint64
}

type pencodeSettingsWriteResult struct {
	err      error
	revision uint64
}

func newPencodeSettingsWriter(backend *client.Client, wake func()) *pencodeSettingsWriter {
	w := &pencodeSettingsWriter{requests: make(chan pencodeSettingsWrite, 1), results: make(chan pencodeSettingsWriteResult, 1), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		for request := range w.requests {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := backend.SaveSettings(ctx, request.data)
			cancel()
			select {
			case <-w.results:
			default:
			}
			w.results <- pencodeSettingsWriteResult{err: err, revision: request.revision}
			if wake != nil {
				wake()
			}
		}
	}()
	return w
}

func (model *workspaceModel) saveRemoteSettings() {
	if model.session == nil || !model.backendConnected() {
		model.settingsFeedback = "远程连接已断开，设置未保存"
		return
	}
	data, err := json.MarshalIndent(model.settings, "", "  ")
	if err != nil {
		model.settingsFeedback = fmt.Sprintf("保存设置失败：%v", err)
		return
	}
	s := model.session
	if s.settingsWriters == nil {
		s.settingsWriters = make(map[*client.Client]*pencodeSettingsWriter)
	}
	w := s.settingsWriters[model.backend]
	if w == nil {
		w = newPencodeSettingsWriter(model.backend, model.backgroundWake())
		s.settingsWriters[model.backend] = w
	}
	select {
	case <-w.requests:
	default:
	}
	w.revision++
	w.requests <- pencodeSettingsWrite{data: append(data, '\n'), revision: w.revision}
	model.settingsFeedback = "正在保存远程设置…"
}

func (model *workspaceModel) pollSettingsWrites() bool {
	if model.session == nil {
		return false
	}
	w := model.session.settingsWriters[model.backend]
	if w == nil {
		return false
	}
	select {
	case result := <-w.results:
		if result.revision != w.revision {
			return false
		}
		model.settingsFeedback = ""
		if result.err != nil {
			model.settingsFeedback = fmt.Sprintf("保存远程设置失败：%v", result.err)
		}
		return true
	default:
		return false
	}
}

func (s *Session) closeSettingsWriters() {
	// Close all queues first so their bounded final writes finish concurrently.
	for _, w := range s.settingsWriters {
		close(w.requests)
	}
	for _, w := range s.settingsWriters {
		<-w.done
	}
	clear(s.settingsWriters)
}

func (s *Session) closeSettingsWriter(backend *client.Client) {
	if w := s.settingsWriters[backend]; w != nil {
		close(w.requests)
		<-w.done
		delete(s.settingsWriters, backend)
	}
}
