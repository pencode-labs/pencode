package desktopopen

/*
#cgo pkg-config: x11
#include <X11/Xlib.h>

static void focusWindow(Window window) {
	Display *display = XOpenDisplay(NULL);
	if (!display) return;
	Window root = DefaultRootWindow(display);
	XEvent event = {0};
	event.xclient.type = ClientMessage;
	event.xclient.window = window;
	event.xclient.message_type = XInternAtom(display, "_NET_ACTIVE_WINDOW", False);
	event.xclient.format = 32;
	event.xclient.data.l[0] = 1;
	event.xclient.data.l[1] = CurrentTime;
	XMapRaised(display, window);
	XSendEvent(display, root, False, SubstructureRedirectMask | SubstructureNotifyMask, &event);
	// Bare X servers have no window manager to handle the client message.
	if (XInternAtom(display, "_NET_SUPPORTING_WM_CHECK", True) == None) {
		XSetInputFocus(display, window, RevertToParent, CurrentTime);
	}
	XCloseDisplay(display);
}
*/
import "C"
import "github.com/onix-engine/onix/window"

func Focus(context *window.Context) {
	if id := context.NativeX11Window(); id != 0 {
		C.focusWindow(C.Window(id))
	}
}
