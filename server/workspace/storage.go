package workspace

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"

	"github.com/pencode-labs/pencode/configuration"
	"github.com/pencode-labs/pencode/internal/ripgrep"
	"github.com/pencode-labs/pencode/protocol"
)

const profileParent = protocol.StorageKinds

var profileSession = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z-(gui|server)-[0-9]+-[0-9]+$`)

type storageScan struct {
	ctx    context.Context
	areas  map[string]protocol.StorageKind
	report protocol.StorageReport
	clean  protocol.StorageMask
}

// DiskStorage measures host-owned data. Only fixed categories are accepted on the wire.
func DiskStorage(ctx context.Context, resources, extensions string, clean protocol.StorageMask) (protocol.StorageReport, error) {
	s := storageScan{ctx: ctx, clean: clean, areas: make(map[string]protocol.StorageKind)}
	if err := ctx.Err(); err != nil {
		return s.report, err
	}
	if clean & ^protocol.CleanableStorage != 0 {
		return s.report, protocol.ErrProtocol
	}
	s.report.Host, _ = os.Hostname()
	config, err := os.UserConfigDir()
	if err != nil {
		return s.report, err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return s.report, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return s.report, err
	}
	config, cache, home = filepath.Join(config, "pencode"), filepath.Join(cache, "pencode"), filepath.Join(home, ".pencode")
	s.areas[home], s.areas[cache], s.areas[config] = protocol.StorageOther, protocol.StorageTools, protocol.StorageOther
	s.areas[filepath.Join(config, "local-history")] = protocol.StorageHistory
	s.areas[filepath.Join(cache, "ripgrep")] = protocol.StorageSearchCache
	s.areas[filepath.Join(home, "extensions")] = protocol.StorageExtensions
	s.areas[filepath.Join(home, "profiles")] = profileParent
	if extensions != "" {
		extensions, err = filepath.Abs(extensions)
		if err != nil {
			return s.report, err
		}
		s.areas[extensions] = protocol.StorageExtensions
	}
	if resources != "" {
		resources, err = filepath.Abs(resources)
		if err != nil {
			return s.report, err
		}
		s.areas[resources] = protocol.StorageOther
		for _, name := range []string{"debug-adapters", "language-servers"} {
			s.areas[filepath.Join(resources, name)] = protocol.StorageTools
		}
	}
	if parent := os.Getenv("PENCODE_PROFILE_DIR"); parent != "" {
		// A custom parent can be shared with unrelated applications.
		parent, err = filepath.Abs(parent)
		if err != nil {
			return s.report, err
		}
		entries, err := os.ReadDir(parent)
		s.note(err)
		for _, e := range entries {
			if e.IsDir() && profileSession.MatchString(e.Name()) {
				s.areas[filepath.Join(parent, e.Name())] = protocol.StorageOtherProfiles
			}
		}
	}
	for path, kind := range s.areas {
		// Nested areas are classified when encountered by their parent walk.
		if s.hasParent(path) {
			continue
		}
		info, err := os.Lstat(path)
		if err == nil && info.IsDir() {
			root, openErr := os.OpenRoot(path)
			if openErr == nil {
				s.walk(root, kind)
				root.Close()
			} else {
				s.note(openErr)
			}
		} else {
			s.note(err)
		}
		if err := ctx.Err(); err != nil {
			return s.report, err
		}
	}
	return s.report, nil
}

func (s *storageScan) hasParent(path string) bool {
	for parent := filepath.Dir(path); parent != path; parent = filepath.Dir(path) {
		if _, ok := s.areas[parent]; ok {
			return true
		}
		path = parent
	}
	return false
}

func (s *storageScan) note(err error) {
	if err != nil && !errors.Is(err, fs.ErrNotExist) && s.report.Problem == "" {
		s.report.Problem = err.Error()
	}
}

// walk returns whether the directory is empty after cleaning. OpenRoot anchors
// every deletion to its parent, and symbolic links are never traversed.
func (s *storageScan) walk(dir *os.Root, kind protocol.StorageKind) bool {
	k, areaRoot := s.areas[dir.Name()]
	if areaRoot {
		kind = k
		if k == protocol.StorageHistory {
			if s.clean&(protocol.StorageHistory.Mask()|protocol.StorageLargeHistory.Mask()) != 0 {
				historyMoves.Lock()
				defer historyMoves.Unlock()
			} else {
				historyMoves.RLock()
				defer historyMoves.RUnlock()
			}
		}
	}
	finished := kind == protocol.StorageOtherProfiles && profileSession.MatchString(filepath.Base(dir.Name())) && regularStorageFile(dir, "session.json") && (regularStorageFile(dir, "resources-final.pprof") || regularStorageFile(dir, ".complete"))
	if finished {
		kind = protocol.StorageProfiles
	}
	f, err := dir.Open(".")
	if err != nil {
		s.note(err)
		return false
	}
	defer f.Close()
	empty := true
	var markers []fs.FileInfo
	for {
		entries, err := f.ReadDir(128)
		for _, e := range entries {
			if s.ctx.Err() != nil {
				return false
			}
			name := e.Name()
			k := kind
			if k == profileParent {
				k = protocol.StorageOtherProfiles
			}
			if e.IsDir() {
				if kind == protocol.StorageSearchCache && ripgrep.CurrentCache(name) {
					k = protocol.StorageTools
				}
				child, err := dir.OpenRoot(name)
				if err != nil {
					s.note(err)
					empty = false
					continue
				}
				canRemove := s.walk(child, k)
				child.Close()
				if canRemove {
					if err := dir.Remove(name); err != nil && !os.IsNotExist(err) {
						s.note(err)
						empty = false
					}
				} else {
					empty = false
				}
				continue
			}
			if !e.Type().IsRegular() {
				empty = false
				continue
			}
			info, err := e.Info()
			if err != nil {
				s.note(err)
				empty = false
				continue
			}
			size := uint64(info.Size())
			if kind == protocol.StorageHistory {
				switch {
				case revisionTime(name) == 0:
					k = protocol.StorageOther
				case size > uint64(configuration.DefaultLocalHistory().MaxFileSize)*1024:
					k = protocol.StorageLargeHistory
				}
			}
			// Keep completion metadata until other files are gone so a cancelled
			// or partially failed cleanup can retry the remaining report.
			if finished && s.clean&k.Mask() != 0 && (name == "session.json" || name == "resources-final.pprof" || name == ".complete") {
				markers = append(markers, info)
				continue
			}
			if !s.file(dir, info, k, s.clean&k.Mask() != 0) {
				empty = false
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.note(err)
				return false
			}
			break
		}
	}
	removeMarkers := empty && s.ctx.Err() == nil
	for _, info := range markers {
		if !s.file(dir, info, kind, removeMarkers) {
			empty = false
		}
	}
	return empty && (!areaRoot || finished) && (s.clean&kind.Mask() != 0 || kind == protocol.StorageHistory && s.clean&protocol.StorageLargeHistory.Mask() != 0)
}

func (s *storageScan) file(dir *os.Root, info fs.FileInfo, kind protocol.StorageKind, remove bool) bool {
	size := uint64(info.Size())
	if remove {
		switch err := dir.Remove(info.Name()); {
		case err == nil:
			s.report.Removed.Files++
			s.report.Removed.Bytes += size
			return true
		case os.IsNotExist(err):
			return true
		default:
			s.note(err)
		}
	}
	s.report.Sizes[kind].Bytes += size
	s.report.Sizes[kind].Files++
	return false
}

func regularStorageFile(dir *os.Root, name string) bool {
	info, err := dir.Lstat(name)
	return err == nil && info.Mode().IsRegular()
}
