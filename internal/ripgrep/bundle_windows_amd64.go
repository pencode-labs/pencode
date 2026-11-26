//go:build onix_release || pencode_ripgrep

package ripgrep

import _ "embed"

//go:embed bundle/ripgrep-16.0.0-x86_64-pc-windows-msvc.zip
var archiveData string

const binarySHA256 = "a140ac0ec5346e6aa59e13bdb09f001237632bafcede54c13b885e08efcecf64"
