package server

import (
	"context"
	"encoding/binary"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pencode-labs/pencode/protocol"
)

// Browsing never registers a workspace or touches its document handles.
func browseDirectory(ctx context.Context, directory string, e *protocol.Encoder) error {
	if directory == "~" || strings.HasPrefix(directory, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		directory = filepath.Join(home, filepath.FromSlash(strings.TrimPrefix(directory, "~")))
	}
	if strings.TrimSpace(directory) == "" {
		return &protocol.Error{Code: protocol.Invalid, Message: "请选择项目目录"}
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(absolute)
	if err != nil {
		return err
	}
	e.String(filepath.ToSlash(absolute))
	e.String(filepath.ToSlash(filepath.Dir(absolute)))
	countOffset := len(e.Buffer)
	e.U32(0)
	var count uint32
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		directory := entry.IsDir()
		if entry.Type()&fs.ModeSymlink != 0 {
			info, err := os.Stat(filepath.Join(absolute, entry.Name()))
			directory = err == nil && info.IsDir()
		}
		if directory {
			e.String(entry.Name())
			count++
		}
	}
	binary.LittleEndian.PutUint32(e.Buffer[countOffset:], count)
	return nil
}
