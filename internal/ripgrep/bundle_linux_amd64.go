//go:build onix_release || pencode_ripgrep

package ripgrep

import _ "embed"

//go:embed bundle/ripgrep-16.0.0-x86_64-unknown-linux-musl.tar.gz
var archiveData string

const binarySHA256 = "a60f4ad2206cb0234c077d9789231fd8a0f9895ff2e2dbbb3d2c944dc14b2613"
