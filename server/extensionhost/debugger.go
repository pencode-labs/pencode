package extensionhost

import (
	"fmt"
	"strings"

	"github.com/pencode-labs/pencode/peb"
)

func validateDebugAdapters(adapters []peb.DebugAdapter) error {
	seen := make(map[string]bool, len(adapters))
	for _, a := range adapters {
		if a.ID == "" || len(a.ID) > 128 || strings.ContainsAny(a.ID, "\x00\r\n/\\") || seen[a.ID] || a.Label == "" || a.Command == "" || strings.ContainsRune(a.Command, 0) {
			return fmt.Errorf("invalid debug adapter %q", a.ID)
		}
		seen[a.ID] = true
		for _, arg := range a.Args {
			if strings.ContainsRune(arg, 0) {
				return fmt.Errorf("invalid debug adapter argument")
			}
		}
	}
	return nil
}
