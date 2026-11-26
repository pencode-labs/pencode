//go:build !onix_release && !pencode_ripgrep

package ripgrep

import "os/exec"

func executable() (string, error) { return exec.LookPath("rg") }

// CurrentCache identifies the extracted binary used by this build.
func CurrentCache(name string) bool { return false }
