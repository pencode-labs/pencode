module github.com/pencode-labs/php

go 1.27.0

require (
	github.com/pencode-labs/pencode v0.0.0-20261023153810-d0e8df981beb
	golang.org/x/sys v0.49.0
)

require golang.org/x/net v0.61.0 // indirect

replace github.com/pencode-labs/pencode => ../..
