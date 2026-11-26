//go:build onix_release || pencode_ripgrep

package ripgrep

import _ "embed"

//go:embed bundle/ripgrep-16.0.0-aarch64-unknown-linux-musl.tar.gz
var archiveData string

const binarySHA256 = "f2d163ba909715818890c754b4faef6cff0d67025b503577a4700f8177b8dc12"
