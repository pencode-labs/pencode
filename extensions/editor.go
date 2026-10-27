package extensions

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pencode-labs/pencode/peb"
)

type EditorResult struct {
	Message peb.EditorMessage
	Reply   peb.EditorReply
	Assets  map[string][]byte
	Err     error
}

// EditorSession owns one active document process. All pipe I/O happens on its
// worker; the UI submits at most one outstanding request and never blocks.
type EditorSession struct {
	Results  <-chan EditorResult
	Done     <-chan struct{}
	settings chan peb.SettingValues
	requests chan peb.EditorMessage
	cancel   context.CancelFunc
}

func (editor *Editor) Start(root string, values peb.SettingValues, wake func()) *EditorSession {
	ctx, cancel := context.WithCancel(context.Background())
	results, done := make(chan EditorResult, 1), make(chan struct{})
	s := &EditorSession{Results: results, Done: done, settings: make(chan peb.SettingValues, 1), requests: make(chan peb.EditorMessage, 1), cancel: cancel}
	go func() {
		defer close(done)
		defer close(results)
		defer cancel()
		publish := func(r EditorResult) {
			select {
			case results <- r:
				if wake != nil {
					wake()
				}
			case <-ctx.Done():
			}
		}
		if err := editor.run(ctx, root, values, s.settings, s.requests, publish); err != nil && ctx.Err() == nil {
			publish(EditorResult{Err: err})
		}
	}()
	return s
}

func (s *EditorSession) SubmitSettings(values peb.SettingValues) bool {
	return submitSettings(s.settings, s.Done, values)
}

func (s *EditorSession) Submit(message peb.EditorMessage) bool {
	select {
	case <-s.Done:
		return false
	default:
	}
	select {
	case s.requests <- message:
		return true
	default:
		return false
	}
}

// Close cancels asynchronously; Done closes only after the child is reaped.
func (s *EditorSession) Close() {
	if s != nil {
		s.cancel()
	}
}

func (editor *Editor) run(parent context.Context, root string, values peb.SettingValues, settings <-chan peb.SettingValues, requests <-chan peb.EditorMessage, publish func(EditorResult)) (err error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stream, stderr, closeProcess, err := openExtensionProcess(ctx, editor.remote, root, editor.Output)
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
	// Each exchange is bounded, but an idle extension has no deadline.
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
	c, err := peb.DecodeContributions(f.Payload)
	if err != nil {
		return err
	}
	found := c.ActionService
	for _, candidate := range c.Actions {
		if candidate.ID == editor.LocalID && candidate.Location == peb.OpenWith {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("editor %s is no longer registered", editor.ID)
	}
	values = peb.ResolveSettings(c.Settings, values)
	data, err = exchangeSettings(&r, &w, data, values, 2)
	if err != nil {
		return err
	}
	deadline.Stop()
	resources := viewResources{remote: editor.remote, cache: make(map[string]viewResource)}
	id := uint32(2)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case values := <-settings:
			id++
			if id == 0 {
				id++
			}
			deadline.Reset(StartupTimeout)
			data, err = exchangeSettings(&r, &w, data, values, id)
			deadline.Stop()
			if err != nil {
				return err
			}
		case message := <-requests:
			id++
			if id == 0 {
				id++
			}
			deadline.Reset(StartupTimeout)
			data, err = peb.EncodeEditorMessage(data, message)
			if err != nil {
				return err
			}
			if err = w.Write(peb.EditorRequest, id, data); err != nil {
				return err
			}
			f, err = r.Read()
			if err != nil {
				return err
			}
			deadline.Stop()
			if f.ID != id {
				return peb.ErrProtocol
			}
			if f.Kind == peb.Failure {
				message, decodeErr := peb.DecodeFailure(f.Payload)
				if decodeErr != nil {
					return decodeErr
				}
				return errors.New(message)
			}
			if f.Kind != peb.EditorResponse {
				return peb.ErrProtocol
			}
			reply, decodeErr := peb.DecodeEditorReply(f.Payload)
			if decodeErr != nil {
				return decodeErr
			}
			resourceCtx, finishResources := context.WithTimeout(ctx, StartupTimeout)
			resources.root, resources.document = root, message.Path
			assets, prepareErr := resources.prepare(resourceCtx, reply.View, "")
			finishResources()
			if prepareErr != nil {
				return prepareErr
			}
			publish(EditorResult{Message: message, Reply: reply, Assets: assets})
		}
	}
}
