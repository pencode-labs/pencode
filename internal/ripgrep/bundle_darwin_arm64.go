//go:build onix_release || pencode_ripgrep

package ripgrep

import _ "embed"

//go:embed bundle/ripgrep-16.0.0-aarch64-apple-darwin.tar.gz
var archiveData string

const binarySHA256 = "de3a7aa933fb5f2ddfcbf6043b4276fe59d0df08bc91d9003e64050e834e4b24"
