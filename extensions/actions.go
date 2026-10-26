package extensions

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync/atomic"
	"time"

	"github.com/diiyw/onix/images/svg"
	"github.com/pencode-labs/pencode/actions"
	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/output"
	"github.com/pencode-labs/pencode/peb"
)

const maxActions = 256

// ActionExtension retains a validated process entry for its live button session.
type ActionExtension struct {
	Output  *output.Channel
	ID      string
	Actions []peb.Action
	remote  *client.Extension
}
type ActionResult struct {
	Change       peb.ActionChange
	Notification *peb.Notification
	UI           *peb.UI
	Assets       map[string][]byte
	Icons        map[string][]byte
	Err          error
	// Fatal marks a disconnected process; its actions must be removed.
	Fatal bool
}
type ActionSession struct {
	Results  <-chan ActionResult
	Done     <-chan struct{}
	requests chan actionRequest
	cancel   context.CancelFunc
	remote   *client.Extension
	output   *output.Channel
}

type actionRequest struct {
	action peb.ActionContext
	event  peb.UIEvent
	ui     bool
}

// Editor binds a live Open With action to the extension's document process.
func (s *ActionSession) Editor(a peb.Action) Editor {
	return Editor{Action: a, LocalID: strings.TrimPrefix(a.ID, "extensions/"+s.remote.Info.ID+"/"), remote: s.remote, Output: s.output}
}

func (s *ActionSession) Submit(c peb.ActionContext) bool { return s.submit(actionRequest{action: c}) }
func (s *ActionSession) SubmitUI(event peb.UIEvent) bool {
	return s.submit(actionRequest{event: event, ui: true})
}
func (s *ActionSession) submit(request actionRequest) bool {
	select {
	case <-s.Done:
		return false
	default:
	}
	select {
	case s.requests <- request:
		return true
	default:
		return false
	}
}
func (s *ActionSession) Close() {
	if s != nil {
		s.cancel()
	}
}
func (extension *ActionExtension) Start(wake func()) *ActionSession {
	ctx, cancel := context.WithCancel(context.Background())
	results, done := make(chan ActionResult, 16), make(chan struct{})
	s := &ActionSession{Results: results, Done: done, requests: make(chan actionRequest, 64), cancel: cancel, remote: extension.remote, output: extension.Output}
	go func() {
		defer close(done)
		defer close(results)
		defer cancel()
		publish := func(r ActionResult) {
			select {
			case results <- r:
				if wake != nil {
					wake()
				}
			case <-ctx.Done():
			}
		}
		if err := extension.run(ctx, s.requests, publish); err != nil && ctx.Err() == nil {
			publish(ActionResult{Err: err, Fatal: true})
		}
	}()
	return s
}

// Validate both static registrations and live replacements on the I/O worker.
func loadAction(files *resourceFiles, extensionID string, a peb.Action, icons map[string][]byte) (peb.Action, error) {
	if !validID(a.ID) {
		return a, fmt.Errorf("invalid action id %q", a.ID)
	}
	if err := actions.Validate(a); err != nil {
		return a, err
	}
	if a.Icon != "" {
		if !validResourcePath(a.Icon, true) || path.Ext(a.Icon) != ".svg" {
			return a, fmt.Errorf("invalid action icon %q", a.Icon)
		}
		source := "peb://" + extensionID + "/" + a.Icon
		if _, ok := icons[source]; !ok {
			data, err := files.ReadFile(a.Icon)
			if err != nil {
				return a, err
			}
			config, err := svg.DecodeConfig(bytes.NewReader(data))
			if err != nil {
				return a, err
			}
			if config.Width < 1 || config.Height < 1 || config.Width > 256 || config.Height > 256 {
				return a, fmt.Errorf("action icon dimensions must be 1..256")
			}
			icons[source] = data
		}
		a.Icon = source
	}
	a.ID = "extensions/" + extensionID + "/" + a.ID
	return a, nil
}

func (extension *ActionExtension) run(parent context.Context, requests <-chan actionRequest, publish func(ActionResult)) (err error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stream, stderr, closeProcess, err := openExtensionProcess(ctx, extension.remote, "", extension.Output)
	if err != nil {
		return err
	}
	defer func() {
		cancel()
		closeProcess()
		if err != nil && stderr.used > 0 {
			err = fmt.Errorf("%w: %s", err, strings.TrimSpace(string(stderr.data[:stderr.used])))
		}
	}()
	r, w := peb.Reader{Source: stream}, peb.Writer{Target: stream}
	deadline := time.AfterFunc(StartupTimeout, cancel)
	defer deadline.Stop()
	data, _ := peb.EncodeHello(nil, peb.Hello{Host: "pencode", Capabilities: peb.Capabilities})
	if err = w.Write(peb.Initialize, 1, data); err != nil {
		return err
	}
	f, err := r.Read()
	if err != nil {
		return err
	}
	if f.Kind != peb.Register || f.ID != 1 {
		return peb.ErrProtocol
	}
	contributions, err := peb.DecodeContributions(f.Payload)
	if err != nil {
		return err
	}
	if !contributions.ActionService {
		return peb.ErrProtocol
	}
	if err = w.Write(peb.ActionActivate, 0, nil); err != nil {
		return err
	}
	deadline.Stop()
	// Reuse loaded icons across updates; the total retained resource budget is bounded.
	icons := make(map[string][]byte)
	files := resourceFiles{source: extension.remote.FS(ctx, nil), cache: make(map[string][]byte)}
	ids := make(map[string]bool, len(extension.Actions))
	for _, a := range extension.Actions {
		ids[a.ID] = true
	}
	resources := viewResources{remote: extension.remote, cache: make(map[string]viewResource)}
	var pending, responseKind atomic.Uint32
	completions := make(chan uint32, 1)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		requestDeadline := time.AfterFunc(StartupTimeout, cancel)
		defer requestDeadline.Stop()
		requestDeadline.Stop()
		var buffer []byte
		id := uint32(1)
		for {
			select {
			case <-ctx.Done():
				return
			case request := <-requests:
				id++
				if id == 0 {
					id++
				}
				pending.Store(id)
				requestDeadline.Reset(StartupTimeout)
				var writeErr error
				kind, response := peb.ActionInvoke, peb.ActionResponse
				if request.ui {
					kind, response = peb.UIRequest, peb.UIResponse
					buffer, writeErr = peb.EncodeUIEvent(buffer, request.event)
				} else {
					buffer, writeErr = peb.EncodeActionContext(buffer, request.action)
				}
				responseKind.Store(uint32(response))
				if writeErr == nil {
					writeErr = w.Write(kind, id, buffer)
				}
				if writeErr != nil {
					cancel()
					return
				}
				select {
				case <-ctx.Done():
					return
				case replyID := <-completions:
					requestDeadline.Stop()
					if replyID != id {
						cancel()
						return
					}
				}
			}
		}
	}()
	defer func() { cancel(); <-writerDone }()
	for {
		f, err = r.Read()
		if err != nil {
			return err
		}
		switch f.Kind {
		case peb.NotificationUpdate:
			n, decodeErr := peb.DecodeNotification(f.Payload)
			if decodeErr != nil {
				return decodeErr
			}
			if !validID(n.ID) || n.Action != "" && !validID(n.Action) {
				return peb.ErrProtocol
			}
			publish(ActionResult{Notification: &n})
		case peb.UIUpdate:
			ui, decodeErr := peb.DecodeUI(f.Payload)
			if decodeErr != nil {
				return decodeErr
			}
			if !validID(ui.ID) || ui.Anchor != "" && !validID(ui.Anchor) {
				return peb.ErrProtocol
			}
			resourceCtx, finish := context.WithTimeout(ctx, StartupTimeout)
			assets, prepareErr := resources.prepare(resourceCtx, ui.View, ui.ID)
			finish()
			if prepareErr != nil {
				return prepareErr
			}
			publish(ActionResult{UI: &ui, Assets: assets})
		case peb.ActionUpdate:
			change, decodeErr := peb.DecodeActionChange(f.Payload)
			if decodeErr != nil {
				return decodeErr
			}
			if !validID(change.Action.ID) {
				return peb.ErrProtocol
			}
			fullID := "extensions/" + extension.ID + "/" + change.Action.ID
			if change.Remove {
				delete(ids, fullID)
				change.Action.ID = fullID
				publish(ActionResult{Change: change})
				continue
			}
			if !ids[fullID] && len(ids) >= maxActions {
				return fmt.Errorf("too many extension actions")
			}
			before := len(icons)
			change.Action, err = loadAction(&files, extension.ID, change.Action, icons)
			if err != nil {
				return err
			}
			ids[fullID] = true
			var added map[string][]byte
			if len(icons) != before {
				// One new icon per replacement; publish an immutable resource map.
				added = map[string][]byte{change.Action.Icon: icons[change.Action.Icon]}
			}
			publish(ActionResult{Change: change, Icons: added})
		case peb.ActionResponse, peb.UIResponse:
			if responseKind.Load() != uint32(f.Kind) {
				return peb.ErrProtocol
			}
			if !pending.CompareAndSwap(f.ID, 0) {
				return peb.ErrProtocol
			}
			select {
			case completions <- f.ID:
			default:
				return peb.ErrProtocol
			}
		case peb.Failure:
			if !pending.CompareAndSwap(f.ID, 0) {
				return peb.ErrProtocol
			}
			message, decodeErr := peb.DecodeFailure(f.Payload)
			if decodeErr != nil {
				return decodeErr
			}
			select {
			case completions <- f.ID:
			default:
				return peb.ErrProtocol
			}
			publish(ActionResult{Err: errors.New(message)})
		default:
			return peb.ErrProtocol
		}
	}
}
