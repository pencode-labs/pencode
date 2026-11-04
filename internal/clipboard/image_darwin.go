package clipboard

/*
#cgo LDFLAGS: -framework AppKit
#include <stdlib.h>
int pencode_clipboard_image(void **data, int *size);
*/
import "C"

import (
	"context"
	"errors"
	"unsafe"
)

func ReadImage(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var data unsafe.Pointer
	var size C.int
	switch C.pencode_clipboard_image(&data, &size) {
	case 0:
		return nil, nil
	case 1:
		defer C.free(data)
		return C.GoBytes(data, size), nil
	case 2:
		return nil, errImageTooLarge
	default:
		return nil, errors.New("could not encode clipboard image as PNG")
	}
}
