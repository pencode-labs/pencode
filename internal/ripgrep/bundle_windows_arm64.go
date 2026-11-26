//go:build onix_release || pencode_ripgrep

package ripgrep

import _ "embed"

//go:embed bundle/ripgrep-16.0.0-aarch64-pc-windows-msvc.zip
var archiveData string

const binarySHA256 = "ce4647f65e8b7470eff4915df928d4a1760b5f6ed0a35e24d3b1e28b5db61eb9"
