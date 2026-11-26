//go:build ignore

package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/pencode-labs/pencode/internal/ripgrep/download"
	"github.com/pencode-labs/pencode/internal/systemproxy"
)

func main() {
	systemproxy.Configure()
	if err := download.Prepare(".", runtime.GOOS, runtime.GOARCH); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
