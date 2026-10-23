package server

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/diiyw/onix/projects/pencode/peb"
	"github.com/diiyw/onix/projects/pencode/protocol"
	"github.com/diiyw/onix/projects/pencode/server/extensionhost"
	"github.com/diiyw/onix/projects/pencode/transport"
)

const (
	extensionCacheBytes        = 128 << 20
	extensionResourcePageBytes = 16 << 20
)

type extensionEntry struct {
	registration extensionhost.Registration
	root         *os.Root
	upload       *extensionhost.Upload
	committed    bool
	enable       bool
	resourceSize int
	resourceData map[string][]byte
}

type extensionService struct {
	mu            sync.Mutex
	owner         *services
	directory     string
	next          uint64
	entries       map[uint64]*extensionEntry
	closed        bool
	resourceBytes int
}

func (s *extensionService) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for id, entry := range s.entries {
		s.remove(id, entry)
	}
}

func (s *extensionService) remove(id uint64, entry *extensionEntry) {
	delete(s.entries, id)
	s.resourceBytes -= entry.resourceSize
	if entry.root != nil {
		entry.root.Close()
	}
	if entry.upload != nil {
		entry.upload.Close()
	}
	// Revoke every runtime of this package before resources can be replaced.
	s.owner.mu.Lock()
	var closing []*serviceStream
	for key, stream := range s.owner.streams {
		if stream.extension == id {
			delete(s.owner.streams, key)
			closing = append(closing, stream)
		}
	}
	s.owner.mu.Unlock()
	for _, stream := range closing {
		stream.close()
	}
}

func (s *extensionService) revoke(name string, except uint64) {
	for id, entry := range s.entries {
		if id != except && entry.registration.Info.ID == name {
			s.remove(id, entry)
		}
	}
}

func (s *extensionService) pending() int {
	n := 0
	for _, entry := range s.entries {
		if !entry.committed {
			n++
		}
	}
	return n
}

func (s *extensionService) encode(id uint64, entry *extensionEntry, e *protocol.Encoder) error {
	data, err := peb.EncodeContributions(nil, entry.registration.Contributions)
	if err != nil {
		return err
	}
	e.U64(id)
	protocol.EncodeExtension(e, entry.registration.Info)
	e.Bytes(data)
	e.String(entry.registration.Diagnostics)
	return nil
}

func (s *extensionService) register(ctx context.Context, directory, id string, enable bool) (*extensionEntry, error) {
	r, err := extensionhost.Register(ctx, directory, id, enable)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	return &extensionEntry{registration: r, root: root, enable: enable, resourceData: make(map[string][]byte)}, nil
}

func (s *extensionService) dispatch(ctx context.Context, op protocol.Operation, root string, d *protocol.Decoder, e *protocol.Encoder) error {
	// Extension metadata and installation mutations share one owner. Stream I/O
	// uses the independent stream scheduler and never holds this lock.
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || ctx.Err() != nil {
		return context.Canceled
	}
	if s.directory == "" {
		var err error
		s.directory, err = extensionhost.Directory()
		if err != nil {
			return err
		}
	}
	var err error
	s.directory, err = filepath.Abs(s.directory)
	if err != nil {
		return err
	}
	switch op {
	case protocol.ExtensionList:
		if err := d.Done(); err != nil {
			return err
		}
		items, err := extensionhost.List(s.directory)
		if err != nil {
			return err
		}
		e.String(s.directory)
		if len(items) > extensionhost.MaxExtensions {
			return &protocol.Error{Code: protocol.Busy, Message: "extension limit exceeded"}
		}
		e.U32(uint32(len(items)))
		enabled := make(map[string]bool, len(items))
		for _, item := range items {
			enabled[item.ID] = !item.Disabled && item.Problem == ""
			protocol.EncodeExtension(e, item)
		}
		for id, entry := range s.entries {
			if entry.committed && !enabled[entry.registration.Info.ID] {
				s.remove(id, entry)
			}
		}
		return nil
	case protocol.ExtensionLoad, protocol.ExtensionEnable, protocol.ExtensionRemove:
		name := d.String()
		enabled := false
		if op == protocol.ExtensionEnable {
			v := d.U32()
			if v > 1 {
				return protocol.ErrProtocol
			}
			enabled = v == 1
		}
		if err := d.Done(); err != nil {
			return err
		}
		if !extensionhost.ValidID(name) {
			return protocol.ErrProtocol
		}
		directory := filepath.Join(s.directory, name)
		if op == protocol.ExtensionRemove {
			if _, err := os.Lstat(directory); err != nil {
				return err
			}
			s.revoke(name, 0)
			return os.RemoveAll(directory)
		}
		if op == protocol.ExtensionEnable && !enabled {
			item, err := extensionhost.SetDisabled(ctx, s.directory, name, true)
			if err != nil {
				return err
			}
			s.revoke(name, 0)
			protocol.EncodeExtension(e, item)
			return nil
		}
		info, err := extensionhost.Inspect(s.directory, name)
		if err != nil {
			return err
		}
		if info.Disabled && !enabled {
			s.revoke(name, 0)
			return s.encode(0, &extensionEntry{registration: extensionhost.Registration{Info: info}}, e)
		}
		for id, entry := range s.entries {
			if entry.committed && !info.Disabled && entry.registration.Info.ID == name {
				return s.encode(id, entry, e)
			}
		}
		for id, entry := range s.entries {
			if !entry.committed && entry.enable && entry.registration.Info.ID == name {
				s.remove(id, entry)
			}
		}
		if len(s.entries) >= extensionhost.MaxExtensions+2 || enabled && s.pending() >= 2 {
			return &protocol.Error{Code: protocol.Busy, Message: "extension registration budget exceeded"}
		}
		entry, err := s.register(ctx, directory, name, enabled)
		if err != nil {
			return err
		}
		s.next++
		id := s.next
		entry.committed = !enabled
		s.entries[id] = entry
		return s.encode(id, entry, e)
	case protocol.ExtensionBegin:
		if err := d.Done(); err != nil {
			return err
		}
		if s.pending() >= 2 || len(s.entries) >= extensionhost.MaxExtensions+2 {
			return &protocol.Error{Code: protocol.Busy, Message: "at most two extension transactions may be pending"}
		}
		upload, err := extensionhost.Begin(s.directory)
		if err != nil {
			return err
		}
		s.next++
		s.entries[s.next] = &extensionEntry{upload: upload}
		e.U64(s.next)
		return nil
	}

	id := d.U64()
	entry := s.entries[id]
	if d.Err != nil {
		return d.Err
	}
	if entry == nil {
		return &protocol.Error{Code: protocol.NotFound, Message: "extension handle is closed"}
	}
	switch op {
	case protocol.ExtensionWrite:
		name, offset, mode, final, data := d.String(), d.U64(), d.U32(), d.U32(), d.Bytes()
		if err := d.Done(); err != nil {
			return err
		}
		if entry.upload == nil || final > 1 || len(data) > protocol.ChunkSize {
			return protocol.ErrProtocol
		}
		return entry.upload.Write(name, offset, mode, final == 1, data)
	case protocol.ExtensionPrepare:
		if err := d.Done(); err != nil {
			return err
		}
		if entry.upload == nil || entry.root != nil {
			return protocol.ErrProtocol
		}
		r, err := entry.upload.Prepare(ctx)
		if err != nil {
			return err
		}
		entry.registration = r
		entry.root, err = os.OpenRoot(r.Info.Path)
		entry.resourceData = make(map[string][]byte)
		if err != nil {
			return err
		}
		return s.encode(id, entry, e)
	case protocol.ExtensionResource:
		name, limit := d.String(), int64(d.U32())
		if err := d.Done(); err != nil {
			return err
		}
		if entry.root == nil || !extensionhost.ValidResourcePath(name, true) || limit < 1 || limit > extensionhost.MaxFileBytes {
			return protocol.ErrProtocol
		}
		data, err := s.readResource(entry, name, limit)
		if err != nil {
			return err
		}
		e.Bytes(data)
		return nil
	case protocol.ExtensionResources:
		limit, count := int64(d.U32()), d.U32()
		if limit < 1 || limit > extensionhost.MaxFileBytes || count > extensionhost.MaxPackageFiles || count > uint32(len(d.Buffer)/4) {
			return protocol.ErrProtocol
		}
		names := make([]string, count)
		seen := make(map[string]bool, len(names))
		for i := range names {
			names[i] = d.String()
			if !extensionhost.ValidResourcePath(names[i], true) || seen[names[i]] {
				return protocol.ErrProtocol
			}
			seen[names[i]] = true
		}
		if err := d.Done(); err != nil {
			return err
		}
		if entry.root == nil {
			return protocol.ErrProtocol
		}
		countOffset := len(e.Buffer)
		e.U32(0)
		returned := uint32(0)
		for _, name := range names {
			if err := ctx.Err(); err != nil {
				return err
			}
			data, err := s.readResource(entry, name, limit)
			if err != nil {
				return err
			}
			if returned > 0 && len(e.Buffer)+len(name)+len(data)+8 > extensionResourcePageBytes {
				break
			}
			e.String(name)
			e.Bytes(data)
			returned++
		}
		binary.LittleEndian.PutUint32(e.Buffer[countOffset:], returned)
		return nil
	case protocol.ExtensionCommit:
		if err := d.Done(); err != nil {
			return err
		}
		if entry.root == nil {
			return protocol.ErrProtocol
		}
		if !entry.committed {
			if entry.upload != nil {
				target, err := entry.upload.Publish(s.directory, entry.registration.Info.ID)
				if err != nil {
					return err
				}
				entry.registration.Info.Path = target
				entry.upload = nil
			} else if entry.enable {
				item, err := extensionhost.SetDisabled(ctx, s.directory, entry.registration.Info.ID, false)
				if err != nil {
					return err
				}
				entry.registration.Info = item
			}
			entry.committed = true
			s.revoke(entry.registration.Info.ID, id)
		}
		protocol.EncodeExtension(e, entry.registration.Info)
		return nil
	case protocol.ExtensionAbort:
		if err := d.Done(); err != nil {
			return err
		}
		if entry.committed {
			return protocol.ErrProtocol
		}
		s.remove(id, entry)
		return nil
	case protocol.ExtensionProcess, protocol.ExtensionLanguageProcess:
		language := ""
		if op == protocol.ExtensionLanguageProcess {
			language = d.String()
		}
		if err := d.Done(); err != nil {
			return err
		}
		if !entry.committed || entry.registration.Info.Disabled || entry.root == nil || op == protocol.ExtensionLanguageProcess && root == "" {
			return fmt.Errorf("extension is not active")
		}
		return s.openProcess(ctx, id, entry, root, language, e)
	}
	return protocol.ErrProtocol
}

func (s *extensionService) readResource(entry *extensionEntry, name string, limit int64) ([]byte, error) {
	if data, ok := entry.resourceData[name]; ok {
		if int64(len(data)) > limit {
			return nil, fmt.Errorf("oversized extension resource %q", name)
		}
		return data, nil
	}
	limit = min(limit, int64(extensionhost.MaxResourceBytes-entry.resourceSize), int64(extensionCacheBytes-s.resourceBytes))
	data, err := extensionhost.ReadResource(entry.root, name, limit)
	if err != nil {
		return nil, err
	}
	if entry.resourceSize+len(data) > extensionhost.MaxResourceBytes || len(entry.resourceData) >= extensionhost.MaxPackageFiles || s.resourceBytes+len(data) > extensionCacheBytes {
		return nil, &protocol.Error{Code: protocol.Busy, Message: "extension resource budget exceeded"}
	}
	entry.resourceSize += len(data)
	s.resourceBytes += len(data)
	entry.resourceData[name] = data
	return data, nil
}

func (s *extensionService) openProcess(ctx context.Context, id uint64, entry *extensionEntry, root, language string, e *protocol.Encoder) error {
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if root != "" && !s.owner.roots[root] {
		return &protocol.Error{Code: protocol.NotFound, Message: "workspace is closed"}
	}
	if len(s.owner.streams)+2 > 16 {
		return &protocol.Error{Code: protocol.Busy, Message: "process budget exceeded"}
	}
	logs, childLogs, err := os.Pipe()
	if err != nil {
		return err
	}
	var p io.ReadWriteCloser
	if language == "" {
		p, err = extensionhost.Open(entry.root, entry.registration.Info.Path, entry.registration.Info.ExtensionManifest, childLogs)
	} else {
		err = fmt.Errorf("unknown extension language server %q", language)
		for _, server := range entry.registration.Contributions.LanguageServers {
			if server.ID != language {
				continue
			}
			command := server.Command
			if strings.ContainsAny(command, "/\\") && !filepath.IsAbs(command) {
				if !extensionhost.ValidResourcePath(filepath.ToSlash(command), false) {
					break
				}
				f, checkErr := entry.root.Open(command)
				if checkErr != nil {
					err = checkErr
					break
				}
				f.Close()
				command = filepath.Join(entry.registration.Info.Path, command)
			}
			p, err = transport.StartWithOutput(root, command, childLogs, server.Args...)
			break
		}
	}
	childLogs.Close()
	if err != nil {
		logs.Close()
		return err
	}
	s.owner.attachProcess(p, logs, root, id, e)
	return nil
}
