package extensions

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/protocol"
)

// Install uploads a selected local package to the server. Only this explicit
// file-transfer source is opened locally; installed packages always live there.
func Install(ctx context.Context, backend *client.Client, source string) (Installed, *Bundle, error) {
	root, err := os.OpenRoot(source)
	if err != nil {
		return Installed{}, nil, err
	}
	defer root.Close()
	// Catch a wrong selection before creating a remote transaction.
	if _, err := readResource(root.FS(), "resources/extension.json", 64<<10); err != nil {
		return Installed{}, nil, err
	}
	upload, err := backend.BeginExtension(ctx)
	if err != nil {
		return Installed{}, nil, err
	}
	committed := false
	defer func() {
		if !committed {
			cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = upload.Abort(cleanup)
		}
	}()
	if err := uploadExtension(ctx, root.FS(), upload); err != nil {
		return Installed{}, nil, err
	}
	remote, err := upload.Prepare(ctx)
	if err != nil {
		return Installed{}, nil, err
	}
	var bundle *Bundle
	if !remote.Info.Disabled {
		bundle, err = importRemote(ctx, remote)
		if err != nil {
			return Installed{}, nil, err
		}
	}
	item, err := remote.Commit(ctx)
	if err != nil {
		bundle.Close()
		return Installed{}, nil, err
	}
	committed = true
	return item, bundle, nil
}

// Enabling is prepared on the server and committed only after GUI resource
// validation. Failed activation preserves the installed disabled manifest.
func SetEnabled(ctx context.Context, backend *client.Client, id string, enabled bool) (Installed, *Bundle, error) {
	if !enabled {
		item, err := backend.DisableExtension(ctx, id)
		return item, nil, err
	}
	remote, err := backend.PrepareEnableExtension(ctx, id)
	if err != nil {
		return Installed{}, nil, err
	}
	committed := false
	defer func() {
		if !committed {
			cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = remote.Abort(cleanup)
		}
	}()
	bundle, err := importRemote(ctx, remote)
	if err != nil {
		return Installed{}, nil, err
	}
	item, err := remote.Commit(ctx)
	if err != nil {
		bundle.Close()
		return Installed{}, nil, err
	}
	committed = true
	return item, bundle, nil
}

func uploadExtension(ctx context.Context, source fs.FS, upload *client.ExtensionUpload) error {
	const maxBytes = 256 << 20
	const maxFiles = 4096
	buffer := make([]byte, protocol.ChunkSize)
	total, files := int64(0), 0
	return fs.WalkDir(source, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		files++
		if files > maxFiles {
			return fmt.Errorf("扩展文件数量超过 %d", maxFiles)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return upload.Write(ctx, name, 0, uint32(info.Mode()), true, nil)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("扩展包含符号链接或特殊文件：%s", name)
		}
		if info.Size() < 0 || total+info.Size() > maxBytes {
			return fmt.Errorf("扩展大小超过 256 MiB")
		}
		input, err := source.Open(name)
		if err != nil {
			return err
		}
		defer input.Close()
		offset, remaining := uint64(0), info.Size()
		for {
			size := min(int64(len(buffer)), remaining)
			n, err := io.ReadFull(input, buffer[:size])
			if err != nil {
				return err
			}
			remaining -= int64(n)
			final := remaining == 0
			if final {
				var extra [1]byte
				n, err := input.Read(extra[:])
				if n != 0 || err != io.EOF {
					return fmt.Errorf("扩展文件在上传中发生变化：%s", name)
				}
			}
			if err := upload.Write(ctx, name, offset, uint32(info.Mode()), final, buffer[:size]); err != nil {
				return err
			}
			offset += uint64(size)
			total += size
			if final {
				return nil
			}
		}
	})
}
