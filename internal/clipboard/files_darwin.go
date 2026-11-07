package clipboard

/*
#include <stdlib.h>
int pencode_clipboard_has_files(void);
char *pencode_clipboard_files(void);
*/
import "C"

import (
	"context"
	"unsafe"
)

func HasFiles() bool { return C.pencode_clipboard_has_files() != 0 }

func ReadFiles(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data := C.pencode_clipboard_files()
	if data == nil {
		return nil, nil
	}
	defer C.free(unsafe.Pointer(data))
	return fileURIs(C.GoString(data)), nil
}
