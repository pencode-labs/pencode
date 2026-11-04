// Package clipboard reads images without taking ownership of the system clipboard.
package clipboard

import "errors"

// Bound native buffers and decoded images before allocating memory.
const maxImageBytes = 64 << 20

var errImageTooLarge = errors.New("clipboard image exceeds 64 MiB")
