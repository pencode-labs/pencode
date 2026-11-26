// File history belongs to paths on the workspace host, including paths shared by
// nested workspace and repository roots. Only completed snapshots are visible.
package workspace

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/pencode-labs/pencode/configuration"
	"github.com/pencode-labs/pencode/protocol"
)

// The separator after the timestamp records the save source. Older entries
// already use '-' for normal saves.
const (
	historySave          = '-'
	historyBeforeRestore = '_'
	historyRestore       = '+'
)

// FileHistory reads host settings once per settings file revision. An absent
// settings file (or a nil FileHistory) selects the editor defaults.
type FileHistory struct {
	info         fs.FileInfo
	SettingsPath string
	cached       configuration.LocalHistorySettings
	mu           sync.Mutex
}

func (h *FileHistory) settings() configuration.LocalHistorySettings {
	defaults := configuration.DefaultLocalHistory()
	if h == nil || h.SettingsPath == "" {
		return defaults
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	info, err := os.Stat(h.SettingsPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() > configuration.MaxBytes {
		return defaults
	}
	if !sameHistoryFile(h.info, info) {
		data, err := os.ReadFile(h.SettingsPath)
		if err != nil || json.Unmarshal(data, &defaults) != nil {
			return configuration.DefaultLocalHistory()
		}
		defaults.Validate()
		h.info, h.cached = info, defaults
	}
	return h.cached
}

func historyExcluded(patterns map[string]bool, root, name string) bool {
	var absolute string
	for pattern, enabled := range patterns {
		if !enabled {
			continue
		}
		candidate := name
		if filepath.IsAbs(pattern) {
			if absolute == "" {
				absolute = filepath.ToSlash(filepath.Join(root, filepath.FromSlash(name)))
			}
			candidate = absolute
		}
		if matched, _ := doublestar.Match(filepath.ToSlash(pattern), candidate); matched {
			return true
		}
	}
	return false
}

// Snapshot operations on separate files may run concurrently. Moving a history
// subtree must wait for operations that still use its old location.
var (
	historyMoves sync.RWMutex
	historyLocks [32]sync.Mutex
)

func historyLock(dir string) *sync.Mutex {
	key := sha256.Sum256([]byte(dir))
	return &historyLocks[int(key[0])%len(historyLocks)]
}

func HistoryDirectory() (string, error) {
	config, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(config, "pencode", "local-history"), nil
}

func historyLocation(root, name string) (string, error) {
	if name != "" && (!validPath(name) || !filepath.IsLocal(filepath.FromSlash(name))) {
		return "", protocol.ErrProtocol
	}
	archive, err := HistoryDirectory()
	if err != nil {
		return "", err
	}
	full := filepath.Join(root, filepath.FromSlash(name))
	if rel, err := filepath.Rel(archive, full); err == nil && filepath.IsLocal(rel) {
		return "", nil
	}
	volume := filepath.VolumeName(full)
	// Alternating "tree" and path components keeps arbitrary workspace names
	// separate from revision files, even if a file later becomes a directory.
	base := filepath.Join(archive, fmt.Sprintf("%x", sha256.Sum256([]byte(volume))))
	tail := strings.TrimLeft(filepath.ToSlash(full[len(volume):]), "/")
	if tail != "" {
		base = filepath.Join(base, "tree", filepath.FromSlash(strings.ReplaceAll(tail, "/", "/tree/")))
	}
	return base, nil
}

func revisionTime(name string) uint64 {
	if len(name) != 16+1+3+1+64 || name[20] != '-' || (name[16] != historySave && name[16] != historyRestore && name[16] != historyBeforeRestore) {
		return 0
	}
	stamp, _ := strconv.ParseUint(name[:16], 16, 64)
	return stamp
}

// Saved records a successful editor or file-service save.
func (h *FileHistory) Saved(root *os.Root, name string, size int64, known *[sha256.Size]byte) {
	if err := captureHistory(root, name, size, known, historySave, h.settings()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Printf("local history %s: %v", name, err)
	}
}

// readHistoryDir separates sorted revisions from the optional subtree in one pass.
func readHistoryDir(dir string) (revisions []os.DirEntry, tree bool, err error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	revisions = entries[:0]
	for _, e := range entries {
		if e.IsDir() {
			tree = tree || e.Name() == "tree"
		} else if revisionTime(e.Name()) != 0 {
			revisions = append(revisions, e)
		}
	}
	return revisions, tree, nil
}

func pruneHistory(dir string, entries []os.DirEntry, keep int) ([]os.DirEntry, error) {
	for len(entries) > keep {
		if err := os.Remove(filepath.Join(dir, entries[0].Name())); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		entries = entries[1:]
	}
	return entries, nil
}

func captureHistory(root *os.Root, name string, size int64, known *[sha256.Size]byte, source byte, settings configuration.LocalHistorySettings) error {
	maxSize := settings.MaxFileSize * 1024
	if !settings.Enabled || size > maxSize || historyExcluded(settings.Exclude, root.Name(), name) {
		return nil
	}
	dir, err := historyLocation(root.Name(), name)
	if err != nil || dir == "" {
		return err
	}
	historyMoves.RLock()
	defer historyMoves.RUnlock()
	lock := historyLock(dir)
	lock.Lock()
	defer lock.Unlock()
	info, err := root.Lstat(filepath.FromSlash(name))
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxSize {
		return err
	}
	entries, _, err := readHistoryDir(dir)
	if err != nil {
		return err
	}
	entries, err = pruneHistory(dir, entries, settings.MaxFileEntries)
	if err != nil || settings.MaxFileEntries == 0 {
		return err
	}
	var latest string
	if len(entries) > 0 {
		latest = entries[len(entries)-1].Name()
		if known != nil && latest[17:] == fmt.Sprintf("%03o-%x", info.Mode().Perm(), *known) {
			return nil
		}
	} else if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	in, err := root.Open(filepath.FromSlash(name))
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(dir, ".capture-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	b := fileBuffers.Get().(*fileBuffer)
	defer fileBuffers.Put(b)
	b.hash.Reset()
	_, err = io.CopyBuffer(io.MultiWriter(tmp, b.hash), io.LimitReader(in, info.Size()), b.data[:])
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	current, err := root.Lstat(filepath.FromSlash(name))
	if err != nil {
		return err
	}
	if !sameHistoryFile(info, current) {
		return failure(protocol.Conflict, "记录历史期间文件已改变")
	}
	suffix := fmt.Sprintf("%03o-%x", info.Mode().Perm(), b.hash.Sum(b.sum[:0]))
	if latest != "" && latest[17:] == suffix {
		return nil
	}
	lastTime := revisionTime(latest)
	stamp := max(uint64(time.Now().UnixNano()), lastTime+1)
	if err = os.Rename(tmp.Name(), filepath.Join(dir, fmt.Sprintf("%016x%c%s", stamp, source, suffix))); err != nil {
		return err
	}
	if latest != "" && latest[16] == source && stamp-lastTime <= uint64(settings.MergeWindow)*uint64(time.Second) {
		if err := os.Remove(filepath.Join(dir, latest)); err != nil && !os.IsNotExist(err) {
			return err
		}
		entries = entries[:len(entries)-1]
	}
	_, err = pruneHistory(dir, entries, settings.MaxFileEntries-1)
	return err
}

// Moves operate on the archive only, including when an entire workspace folder
// is renamed. An untouched file has no history to move.
func (h *FileHistory) move(fromProject *project, from string, toProject *project, to string) {
	source, err := historyLocation(fromProject.info.Path, from)
	if err != nil || source == "" {
		return
	}
	target, err := historyLocation(toProject.info.Path, to)
	if err != nil || target == "" || source == target {
		return
	}
	historyMoves.Lock()
	defer historyMoves.Unlock()
	info, err := os.Stat(source)
	if err == nil {
		if targetInfo, err := os.Stat(target); err == nil && os.SameFile(info, targetInfo) {
			// A case-only rename on a case-insensitive volume.
			err = os.Rename(source, target)
			if err != nil {
				log.Printf("move local history %s to %s: %v", from, to, err)
			}
			return
		}
		if err = os.MkdirAll(filepath.Dir(target), 0700); err == nil {
			err = moveHistoryDir(source, target, h.settings().MaxFileEntries)
		}
	}
	if err != nil && !os.IsNotExist(err) {
		log.Printf("move local history %s to %s: %v", from, to, err)
	}
}

func moveHistoryDir(source, target string, keep int) error {
	if _, err := os.Lstat(target); os.IsNotExist(err) {
		return os.Rename(source, target)
	} else if err != nil {
		return err
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	existing, _, err := readHistoryDir(target)
	if err != nil {
		return err
	}
	// Time is the version ID within a path. Two formerly distinct files
	// may have been saved at the same instant.
	used := make(map[uint64]bool, len(existing))
	for _, e := range existing {
		used[revisionTime(e.Name())] = true
	}
	for _, e := range entries {
		old, next := filepath.Join(source, e.Name()), filepath.Join(target, e.Name())
		if e.IsDir() {
			err = moveHistoryDir(old, next, keep)
		} else {
			if stamp := revisionTime(e.Name()); stamp != 0 {
				id := stamp
				for used[id] {
					id++
				}
				used[id] = true
				if id != stamp {
					next = filepath.Join(target, fmt.Sprintf("%016x%s", id, e.Name()[16:]))
				}
			}
			err = os.Rename(old, next)
		}
		if err != nil {
			return err
		}
	}
	entries, _, err = readHistoryDir(target)
	if err == nil {
		_, err = pruneHistory(target, entries, keep)
	}
	if err != nil {
		return err
	}
	return os.Remove(source)
}

func compareVersions(a, b protocol.FileVersion) int {
	if n := cmp.Compare(b.Time, a.Time); n != 0 {
		return n
	}
	return cmp.Compare(a.Path, b.Path)
}

func (h *FileHistory) List(ctx context.Context, root, scope string, recursive bool, before protocol.FileVersion) ([]protocol.FileVersion, bool, error) {
	dir, err := historyLocation(root, scope)
	if err != nil || dir == "" {
		return nil, false, err
	}
	historyMoves.RLock()
	defer historyMoves.RUnlock()
	keep := h.settings().MaxFileEntries
	const limit = protocol.FileHistoryPageSize
	var versions []protocol.FileVersion
	var visit func(string, string) error
	visit = func(dir, name string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		lock := historyLock(dir)
		lock.Lock()
		entries, tree, err := readHistoryDir(dir)
		if err == nil {
			entries, err = pruneHistory(dir, entries, keep)
		}
		lock.Unlock()
		if err != nil {
			return err
		}
		for _, entry := range entries {
			v := protocol.FileVersion{Path: name, Time: revisionTime(entry.Name())}
			if before.Time == 0 || compareVersions(v, before) > 0 {
				versions = append(versions, v)
			}
		}
		if recursive && tree {
			children, err := os.ReadDir(filepath.Join(dir, "tree"))
			if err != nil {
				return err
			}
			for _, child := range children {
				if child.IsDir() {
					if err := visit(filepath.Join(dir, "tree", child.Name()), path.Join(name, child.Name())); err != nil {
						return err
					}
				}
			}
		}
		if len(versions) > 2*limit {
			slices.SortFunc(versions, compareVersions)
			versions = versions[:limit+1]
		}
		return nil
	}
	if err = visit(dir, scope); err != nil {
		return nil, false, err
	}
	slices.SortFunc(versions, compareVersions)
	return versions[:min(len(versions), limit)], len(versions) > limit, nil
}

func openFileHistory(root string, v protocol.FileVersion) (*os.File, error) {
	historyMoves.RLock()
	defer historyMoves.RUnlock()
	if v.Path == "" || v.Time == 0 {
		return nil, protocol.ErrProtocol
	}
	dir, err := historyLocation(root, v.Path)
	if err != nil || dir == "" {
		return nil, protocol.ErrProtocol
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if revisionTime(entry.Name()) == v.Time {
			return os.Open(filepath.Join(dir, entry.Name()))
		}
	}
	return nil, fs.ErrNotExist
}

func ReadFileHistory(root string, v protocol.FileVersion) ([]byte, error) {
	in, err := openFileHistory(root, v)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	data, err := io.ReadAll(io.LimitReader(in, protocol.FileComparisonLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > protocol.FileComparisonLimit {
		return nil, errors.New("file exceeds the 2 MiB text comparison limit")
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != filepath.Base(in.Name())[21:] {
		return nil, errors.New("文件历史版本校验失败")
	}
	return data, nil
}

func (h *FileHistory) Restore(root *os.Root, v protocol.FileVersion) error {
	in, err := openFileHistory(root.Name(), v)
	if err != nil {
		return err
	}
	defer in.Close()
	revision := filepath.Base(in.Name())
	mode, err := strconv.ParseUint(revision[17:20], 8, 32)
	if err != nil {
		return err
	}
	settings := h.settings()
	dest := filepath.FromSlash(v.Path)
	info, err := root.Lstat(dest)
	if err == nil {
		if !info.Mode().IsRegular() {
			return failure(protocol.Conflict, "目标不是普通文件")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = root.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(dest), SaveTemporaryPrefix+rand.Text())
	out, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	hash := sha256.New()
	var size int64
	if size, err = io.Copy(io.MultiWriter(out, hash), in); err == nil && hex.EncodeToString(hash.Sum(nil)) != revision[21:] {
		err = errors.New("文件历史版本校验失败")
	}
	if err == nil {
		err = out.Chmod(fs.FileMode(mode))
	}
	if err == nil {
		err = out.Sync()
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	// Pruning while backing up the current contents may remove the selected
	// revision. Release its handle first, especially on Windows.
	in.Close()
	if info != nil {
		if err := captureHistory(root, v.Path, info.Size(), nil, historyBeforeRestore, settings); err != nil {
			return err
		}
	}
	current, err := root.Lstat(dest)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if !sameHistoryFile(info, current) {
		return failure(protocol.Conflict, "恢复期间文件已改变")
	}
	if err := root.Rename(tmp, dest); err != nil {
		return err
	}
	if err := captureHistory(root, v.Path, size, nil, historyRestore, settings); err != nil {
		log.Printf("local history %s: %v", v.Path, err)
	}
	return nil
}

func sameHistoryFile(a, b fs.FileInfo) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime()) && a.Mode() == b.Mode()
}
