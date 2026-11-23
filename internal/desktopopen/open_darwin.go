package desktopopen

/*
#cgo LDFLAGS: -framework AppKit
#include <stdint.h>
void pencode_configure_document_events(void);
void pencode_focus_window(uintptr_t handle);
*/
import "C"
import "github.com/onix-engine/onix/window"

var openPath func(string)

// Configure must run on the window thread before the window is created.
func Configure(open func(string)) {
	openPath = open
	C.pencode_configure_document_events()
}

//export pencode_open_document
func pencode_open_document(path *C.char) {
	if openPath != nil && path != nil {
		openPath(C.GoString(path))
	}
}

func Focus(context *window.Context) {
	C.pencode_focus_window(C.uintptr_t(context.NativeHandle()))
}
