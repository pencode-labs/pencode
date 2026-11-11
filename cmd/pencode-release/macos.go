package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
)

// Editor inputs never request one-time codes. Opt out before distributing the
// bundle so macOS doesn't launch its AutoFill helper for ordinary text input.
func configureMacOSBundle(bundle string) error {
	for _, command := range []*exec.Cmd{
		exec.Command("/usr/bin/plutil", "-replace", "NSAutoFillRequiresTextContentTypeForOneTimeCodeOnMac", "-bool", "true", filepath.Join(bundle, "Contents", "Info.plist")),
		exec.Command("/usr/bin/plutil", "-replace", "CFBundleDocumentTypes", "-xml", macOSDocumentTypes, filepath.Join(bundle, "Contents", "Info.plist")),
		// Changing Info.plist invalidates the signature produced by Onix.
		exec.Command("/usr/bin/codesign", "--force", "--sign", "-", bundle),
	} {
		if output, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf("configure macOS bundle: %s: %w: %s", command.Path, err, output)
		}
	}
	return nil
}
