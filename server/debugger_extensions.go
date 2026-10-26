package server

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/pencode-labs/pencode/server/extensionhost"
)

// Called with the extension lock held. The caller observes the registration's
// lifetime while starting the process and rechecks it before attaching streams.
func (s *extensionService) debugAdapter(adapter string) (command string, args []string, handle uint64, err error) {
	for id, entry := range s.entries {
		if !entry.committed {
			continue
		}
		for _, contribution := range entry.registration.Contributions.DebugAdapters {
			if contribution.ID != adapter {
				continue
			}
			if handle != 0 {
				return "", nil, 0, fmt.Errorf("multiple extensions provide debug adapter %q", adapter)
			}
			command, args, handle = contribution.Command, contribution.Args, id
			if strings.ContainsAny(command, "/\\") && !filepath.IsAbs(command) {
				if !extensionhost.ValidResourcePath(filepath.ToSlash(command), false) {
					return "", nil, 0, fmt.Errorf("invalid debug adapter command %q", command)
				}
				file, err := entry.root.Open(command)
				if err != nil {
					return "", nil, 0, err
				}
				file.Close()
				command = filepath.Join(entry.registration.Info.Path, command)
			}
		}
	}
	return
}
