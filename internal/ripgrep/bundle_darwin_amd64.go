//go:build onix_release || pencode_ripgrep

package ripgrep

import _ "embed"

//go:embed bundle/ripgrep-16.0.0-x86_64-apple-darwin.tar.gz
var archiveData string

const binarySHA256 = "dabb6baa856f1c84690d87485618f536d13412fab097057390cce46988832cb4"
