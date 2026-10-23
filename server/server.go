// Package server serves Pencode workspaces over a binary byte stream. It has no
// dependency on the engine's rendering, input, ECS or window packages.
package server

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/pencode-labs/pencode/protocol"
	"github.com/pencode-labs/pencode/server/workspace"
)

type Options struct {
	// Empty selects ~/.pencode/extensions on the server host.
	ExtensionDirectory string
	// Empty selects the server user's system config directory/pencode/resources.
	ConfigurationDirectory string
}

// Serve preserves wire order for document mutations. Long operations run in a
// bounded service pool; cancellation and ordinary file edits keep flowing.
func Serve(parent context.Context, conn io.ReadWriteCloser, options Options) error {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	defer conn.Close()
	var store workspace.Store
	defer store.Close()
	services := newServices(ctx, options)
	var workers sync.WaitGroup
	var activeMu sync.Mutex
	active := make(map[uint64]context.CancelFunc)
	activeBytes := 0
	defer func() { cancel(); services.close(); workers.Wait() }()
	r := protocol.Reader{Source: conn}
	output := newResponses(ctx)
	written := make(chan struct{})
	go func() {
		defer close(written)
		if output.run(conn) != nil {
			cancel()
		}
	}()
	defer func() { cancel(); _ = conn.Close(); <-written }()
	initialized := false
	partials := make(map[uint64][]byte)
	partialBytes := 0
	for {
		frame, err := r.Read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if frame.Kind == protocol.Cancel {
			partialBytes -= len(partials[frame.ID])
			delete(partials, frame.ID)
			if len(frame.Payload) != 0 {
				return protocol.ErrProtocol
			}
			output.cancel(frame.ID)
			activeMu.Lock()
			stop := active[frame.ID]
			activeMu.Unlock()
			if stop != nil {
				stop()
			}
			continue
		}

		if frame.Kind == protocol.Data {
			partial := partials[frame.ID]
			if !initialized || len(partial)+len(frame.Payload) > protocol.MaxMessage || partialBytes+len(frame.Payload) > 128<<20 || len(partials) >= 64 && partial == nil {
				return protocol.ErrProtocol
			}
			partials[frame.ID] = append(partial, frame.Payload...)
			partialBytes += len(frame.Payload)
			continue
		}
		if frame.Kind >= protocol.Reply {
			return protocol.ErrProtocol
		}
		if partial, ok := partials[frame.ID]; ok {
			if len(partial)+len(frame.Payload) > protocol.MaxMessage {
				return protocol.ErrProtocol
			}
			partialBytes -= len(partial)
			delete(partials, frame.ID)
			frame.Payload = append(partial, frame.Payload...)
		}

		var e *protocol.Encoder
		if !initialized {
			if frame.Kind != protocol.Hello || len(frame.Payload) != 0 {
				return protocol.ErrProtocol
			}
			e = output.encoder()
			initialized = true
		} else if frame.Kind == protocol.Service {
			activeMu.Lock()
			if len(active) >= 48 || activeBytes+len(frame.Payload) > 128<<20 {
				activeMu.Unlock()
				output.reply(frame.ID, output.encoder(), &protocol.Error{Code: protocol.Busy, Message: "service queue is full"})
				continue
			}
			if active[frame.ID] != nil {
				activeMu.Unlock()
				return protocol.ErrProtocol
			}
			jobCtx, stop := context.WithCancel(ctx)
			active[frame.ID] = stop
			activeBytes += len(frame.Payload)
			activeMu.Unlock()
			payload := append([]byte(nil), frame.Payload...)
			workers.Add(1)
			go func(id uint64) {
				defer workers.Done()
				defer stop()
				defer func() { activeMu.Lock(); delete(active, id); activeBytes -= len(payload); activeMu.Unlock() }()
				result := output.encoder()
				err := services.dispatch(jobCtx, payload, result)
				output.reply(id, result, err)
			}(frame.ID)
			continue
		} else {
			e = output.encoder()
			err = dispatch(&store, frame, e)
			if err == nil && frame.Kind == protocol.WorkspaceOpen {
				d := protocol.Decoder{Buffer: e.Buffer}
				id := d.U64()
				root := d.String()
				services.mu.Lock()
				services.roots[root] = true
				services.projects[id] = root
				services.mu.Unlock()
			}
			if err == nil && frame.Kind == protocol.WorkspaceClose {
				d := protocol.Decoder{Buffer: frame.Payload}
				services.closeWorkspace(d.U64())
			}
		}
		output.reply(frame.ID, e, err)
	}
}

func dispatch(s *workspace.Store, f protocol.Frame, e *protocol.Encoder) error {
	d := protocol.Decoder{Buffer: f.Payload}
	switch f.Kind {
	case protocol.WorkspaceOpen:
		name := d.String()
		if err := d.Done(); err != nil {
			return err
		}
		p, err := s.Open(name)
		if err != nil {
			return err
		}
		e.U64(p.ID)
		e.String(p.Path)
		e.String(p.Name)
	case protocol.WorkspaceClose:
		id := d.U64()
		if err := d.Done(); err != nil {
			return err
		}
		return s.CloseProject(id)
	case protocol.DocumentOpen:
		id, name, text := d.U64(), d.String(), d.U32()
		if err := d.Done(); err != nil {
			return err
		}
		if text > 1 {
			return protocol.ErrProtocol
		}
		doc, err := s.OpenDocument(id, name, text == 1)
		if err != nil {
			return err
		}
		e.U64(doc.ID)
		e.U64(doc.Version)
		e.String(doc.Content)
	case protocol.DocumentChange:
		id, version, start, end, text := d.U64(), d.U64(), d.U32(), d.U32(), d.String()
		if err := d.Done(); err != nil {
			return err
		}
		version, err := s.Change(id, version, start, end, text)
		if err != nil {
			return err
		}
		e.U64(version)
	case protocol.DocumentSave:
		id, version := d.U64(), d.U64()
		if err := d.Done(); err != nil {
			return err
		}
		return s.Save(id, version)
	case protocol.DocumentClose:
		id := d.U64()
		if err := d.Done(); err != nil {
			return err
		}
		s.CloseDocument(id)
	case protocol.DocumentMove:
		id, project, name := d.U64(), d.U64(), d.String()
		if err := d.Done(); err != nil {
			return err
		}
		return s.Move(id, project, name)
	case protocol.DirectoryRead:
		id, name := d.U64(), d.String()
		if err := d.Done(); err != nil {
			return err
		}
		entries, err := s.ReadDirectory(id, name)
		if err != nil {
			return err
		}
		e.U32(uint32(len(entries)))
		for _, entry := range entries {
			e.String(entry.Name)
			e.U32(entry.Mode)
		}
	case protocol.ResourceCreate:
		id, name, directory := d.U64(), d.String(), d.U32()
		if err := d.Done(); err != nil {
			return err
		}
		if directory > 1 {
			return protocol.ErrProtocol
		}
		return s.Create(id, name, directory == 1)
	case protocol.ResourceRemove:
		id, name := d.U64(), d.String()
		if err := d.Done(); err != nil {
			return err
		}
		return s.Remove(id, name)
	case protocol.ResourceRename:
		id, from, to := d.U64(), d.String(), d.String()
		if err := d.Done(); err != nil {
			return err
		}
		return s.Rename(id, from, to)
	case protocol.ResourceTransfer:
		source, target, from, directory, flags := d.U64(), d.U64(), d.String(), d.String(), d.U32()
		if err := d.Done(); err != nil {
			return err
		}
		if flags > 3 {
			return protocol.ErrProtocol
		}
		name, err := s.Transfer(source, target, from, directory, flags&1 != 0, flags&2 != 0)
		if err != nil {
			return err
		}
		e.String(name)
	default:
		return protocol.ErrProtocol
	}
	return nil
}
