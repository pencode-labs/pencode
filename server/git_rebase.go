package server

import (
	"context"
	"os"
	"strings"

	"github.com/pencode-labs/pencode/protocol"
)

func gitRebasing(ctx context.Context, root string, e *protocol.Encoder) error {
	// Resolve on the backend: linked worktrees and submodules keep their
	// per-worktree state outside the checkout's .git directory.
	data, err := command(ctx, root, []string{"rev-parse", "--path-format=absolute", "--git-path", "rebase-merge", "--git-path", "rebase-apply"})
	if err != nil {
		return err
	}
	for name := range strings.SplitSeq(strings.TrimSuffix(string(data), "\n"), "\n") {
		info, err := os.Stat(name)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && info.IsDir() {
			e.U32(1)
			return nil
		}
	}
	e.U32(0)
	return nil
}
