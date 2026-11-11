package desktopopen

/*
#cgo LDFLAGS: -framework AppKit
void pencode_configure_document_events(void);
*/
import "C"

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
