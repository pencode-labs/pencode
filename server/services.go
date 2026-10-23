package server

import (
	"context"
	"path/filepath"
	"slices"
	"sync"

	"github.com/pencode-labs/pencode/configuration"

	"github.com/pencode-labs/pencode/protocol"
	"github.com/diiyw/onix/syntax"
)

type services struct {
	ctx                    context.Context
	mu                     sync.Mutex
	next                   uint64
	roots                  map[string]bool
	projects               map[uint64]string
	derived                map[string]string
	background             sync.WaitGroup
	streams                map[uint64]*serviceStream
	indexes                map[uint64]*serviceIndex
	syntaxMu               sync.Mutex
	syntax                 map[uint64]*syntaxSession
	extensions             *extensionService
	configurationDirectory string
}

func newServices(ctx context.Context, options Options) *services {
	configureSyntaxMemory()
	s := &services{ctx: ctx, roots: make(map[string]bool), projects: make(map[uint64]string), derived: make(map[string]string), streams: make(map[uint64]*serviceStream), indexes: make(map[uint64]*serviceIndex), syntax: make(map[uint64]*syntaxSession)}
	s.extensions = &extensionService{owner: s, directory: options.ExtensionDirectory, entries: make(map[uint64]*extensionEntry)}
	s.configurationDirectory = options.ConfigurationDirectory
	return s
}
func (s *services) close() {
	s.extensions.close()
	s.mu.Lock()
	for _, p := range s.streams {
		p.close()
	}
	for _, p := range s.indexes {
		p.cancel()
	}
	s.mu.Unlock()
	s.background.Wait()
	s.syntaxMu.Lock()
	for _, p := range s.syntax {
		p.document.Close()
	}
	clear(s.syntax)
	syntax.ReleaseParserMemory()
	s.syntaxMu.Unlock()
}
func (s *services) root(name string) error {
	s.mu.Lock()
	allowed := s.roots[name]
	s.mu.Unlock()
	if !allowed {
		return &protocol.Error{Code: protocol.NotFound, Message: "workspace is closed"}
	}
	return nil
}
func (s *services) dispatch(ctx context.Context, payload []byte, e *protocol.Encoder) error {
	d := protocol.Decoder{Buffer: payload}
	op := protocol.Operation(d.U32())
	root := d.String()
	if d.Err != nil {
		return d.Err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if root != "" {
		if err := s.root(root); err != nil {
			return err
		}
	}
	switch op {
	case protocol.ConfigurationRead, protocol.SettingsSave:
		if root != "" {
			return protocol.ErrProtocol
		}
		directory := s.configurationDirectory
		if directory == "" {
			var err error
			directory, err = configuration.Directory()
			if err != nil {
				return err
			}
		}
		if op == protocol.SettingsSave {
			data := d.Bytes()
			if err := d.Done(); err != nil {
				return err
			}
			return configuration.Save(ctx, directory, data)
		}
		if err := d.Done(); err != nil {
			return err
		}
		snapshot, err := configuration.Read(ctx, directory)
		if err != nil {
			return err
		}
		e.String(directory)
		e.U32(uint32(len(snapshot.Files)))
		names := make([]string, 0, len(snapshot.Files))
		for name := range snapshot.Files {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			data := snapshot.Files[name]
			e.String(name)
			e.Bytes(data)
		}
		return nil
	case protocol.DirectoryBrowse:
		directory := d.String()
		if err := d.Done(); err != nil {
			return err
		}
		return browseDirectory(ctx, directory, e)
	case protocol.GitCommand, protocol.FileRead, protocol.FileSave, protocol.ProcessOpen, protocol.TerminalOpen, protocol.IndexOpen:
		if root == "" {
			return protocol.ErrProtocol
		}
	}
	switch op {
	case protocol.GitCommand:
		return s.git(ctx, root, &d, e)
	case protocol.FileRead, protocol.FileSave:
		return s.file(op, root, &d, e)
	case protocol.DiffPatch:
		return s.diff(ctx, &d, e)
	case protocol.ProcessOpen, protocol.TerminalOpen, protocol.StreamRead, protocol.StreamWrite, protocol.StreamResize, protocol.StreamClose, protocol.StreamWait:
		return s.process(ctx, op, root, &d, e)
	case protocol.IndexOpen, protocol.IndexNext, protocol.IndexClose, protocol.PathSearch:
		return s.index(ctx, op, root, &d, e)
	case protocol.SyntaxAnalyze, protocol.SyntaxRelease:
		return s.analyze(ctx, op, &d, e)
	case protocol.ExtensionList, protocol.ExtensionLoad, protocol.ExtensionResource, protocol.ExtensionResources, protocol.ExtensionProcess, protocol.ExtensionLanguageProcess, protocol.ExtensionBegin, protocol.ExtensionWrite, protocol.ExtensionPrepare, protocol.ExtensionCommit, protocol.ExtensionAbort, protocol.ExtensionEnable, protocol.ExtensionRemove:
		return s.extensions.dispatch(ctx, op, root, &d, e)
	default:
		return protocol.ErrProtocol
	}
}

// Closing a workspace revokes its derived repository roots and process handles.
func (s *services) closeWorkspace(id uint64) {
	s.mu.Lock()
	root := s.projects[id]
	delete(s.projects, id)
	delete(s.roots, root)
	for derived, owner := range s.derived {
		if owner == root {
			replacement := ""
			for _, opened := range s.projects {
				if relative, err := filepath.Rel(derived, opened); err == nil && filepath.IsLocal(relative) {
					replacement = opened
					break
				}
			}
			if replacement != "" {
				s.derived[derived] = replacement
			} else {
				delete(s.roots, derived)
				delete(s.derived, derived)
			}
		}
	}
	for _, opened := range s.projects {
		s.roots[opened] = true
	}
	for key, p := range s.indexes {
		if p.root == root {
			p.cancel()
			delete(s.indexes, key)
		}
	}
	for key, p := range s.streams {
		if p.root != "" && !s.roots[p.root] {
			delete(s.streams, key)
			s.background.Add(1)
			go func() { defer s.background.Done(); p.close() }()
		}
	}
	s.mu.Unlock()
}
