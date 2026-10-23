// Package extensionhost owns native extension discovery, registration and files.
// It deliberately has no dependency on the GUI or the client package.
package extensionhost

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pencode-labs/pencode/protocol"
)

const (
	StartupTimeout   = 5 * time.Second
	MaxExtensions    = 64
	MaxResourceBytes = 64 << 20
	MaxFileBytes     = 32 << 20
	MaxPackageBytes  = 256 << 20
	MaxPackageFiles  = 4096
)

// The shared manifest and metadata are also the wire-domain types.
type Manifest = protocol.ExtensionManifest
type Installed = protocol.ExtensionInfo

func Directory() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".pencode", "extensions"), nil
}

func ValidID(id string) bool {
	if len(id) == 0 || len(id) > 128 || id == "." || id == ".." {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

func ValidResourcePath(name string, resource bool) bool {
	return fs.ValidPath(name) && name != "." && !strings.ContainsAny(name, "\\:?#\x00") &&
		(!resource || strings.HasPrefix(name, "resources/"))
}

// List reads metadata without executing extensions, including disabled and
// broken installations so that users can inspect and remove them.
func List(directory string) ([]Installed, error) {
	dir, err := os.Open(directory)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(MaxPackageFiles + 1)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(entries) > MaxPackageFiles {
		return nil, fmt.Errorf("extension directory exceeds %d entries", MaxPackageFiles)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	items := make([]Installed, 0, min(len(entries), MaxExtensions))
	for _, entry := range entries {
		if !entry.IsDir() || !ValidID(entry.Name()) {
			continue
		}
		if len(items) == MaxExtensions {
			return nil, fmt.Errorf("at most %d extensions can be installed", MaxExtensions)
		}
		item := Installed{ExtensionManifest: Manifest{ID: entry.Name()}, Path: filepath.Join(directory, entry.Name())}
		root, err := os.OpenRoot(item.Path)
		if err == nil {
			var manifest Manifest
			manifest, err = readManifest(root)
			root.Close()
			if err == nil && manifest.ID != item.ID {
				err = fmt.Errorf("扩展 ID 与目录名不一致")
			}
			if err == nil {
				item.ExtensionManifest = manifest
			}
		}
		if err != nil {
			item.Problem = err.Error()
		}
		items = append(items, item)
	}
	return items, nil
}

func Inspect(directory, id string) (Installed, error) {
	if !ValidID(id) {
		return Installed{}, fmt.Errorf("invalid extension id %q", id)
	}
	target := filepath.Join(directory, id)
	root, err := os.OpenRoot(target)
	if err != nil {
		return Installed{}, err
	}
	defer root.Close()
	manifest, err := readManifest(root)
	if err != nil {
		return Installed{}, err
	}
	if manifest.ID != id {
		return Installed{}, fmt.Errorf("manifest id must match directory %q", id)
	}
	return Installed{ExtensionManifest: manifest, Path: target}, nil
}

func readManifest(root *os.Root) (Manifest, error) {
	data, err := ReadResource(root, "resources/extension.json", 64<<10)
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, err
	}
	if !ValidID(manifest.ID) {
		return Manifest{}, fmt.Errorf("无效的扩展 ID：%q", manifest.ID)
	}
	if !ValidResourcePath(manifest.Entry, false) {
		return Manifest{}, fmt.Errorf("无效的扩展入口：%q", manifest.Entry)
	}
	return manifest, nil
}

func SetDisabled(ctx context.Context, directory, id string, disabled bool) (Installed, error) {
	if !ValidID(id) {
		return Installed{}, fmt.Errorf("invalid extension id %q", id)
	}
	target := filepath.Join(directory, id)
	root, err := os.OpenRoot(target)
	if err != nil {
		return Installed{}, err
	}
	defer root.Close()
	manifest, err := readManifest(root)
	if err != nil {
		return Installed{}, err
	}
	if manifest.ID != id {
		return Installed{}, fmt.Errorf("manifest id must match directory")
	}
	manifest.Disabled = disabled
	enabled := !disabled
	// Preserve package metadata outside the host's manifest fields.
	data, err := ReadResource(root, "resources/extension.json", 64<<10)
	if err != nil {
		return Installed{}, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return Installed{}, err
	}
	fields["disabled"] = json.RawMessage("false")
	if !enabled {
		fields["disabled"] = json.RawMessage("true")
	}
	data, err = json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return Installed{}, err
	}
	if len(data)+1 > 64<<10 {
		return Installed{}, fmt.Errorf("扩展清单超过 64 KiB")
	}
	info, err := root.Stat("resources/extension.json")
	if err != nil {
		return Installed{}, err
	}
	stage := "resources/.extension-" + rand.Text() + ".tmp"
	file, err := root.OpenFile(stage, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return Installed{}, err
	}
	defer root.Remove(stage)
	_, writeErr := file.Write(append(data, '\n'))
	closeErr := file.Close()
	if writeErr != nil {
		return Installed{}, writeErr
	}
	if closeErr != nil {
		return Installed{}, closeErr
	}
	if err := ctx.Err(); err != nil {
		return Installed{}, err
	}
	if err := root.Rename(stage, "resources/extension.json"); err != nil {
		return Installed{}, err
	}
	return Installed{ExtensionManifest: manifest, Path: target}, nil
}
func ReadResource(root *os.Root, name string, limit int64) ([]byte, error) {
	f, err := root.Open(filepath.FromSlash(name))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("invalid or oversized resource %q", name)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = fmt.Errorf("oversized resource %q", name)
	}
	return data, err
}
