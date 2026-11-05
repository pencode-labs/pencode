package filewatch

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const Recursive = true
const notificationBufferSize = 16 << 10

type directoryWatch struct {
	stopped chan struct{}
	handle  windows.Handle
	event   windows.Handle
	mu      sync.Mutex
	closing bool
}
type nativeWatcher struct {
	w     *Watcher
	roots map[string]*directoryWatch
	mu    sync.Mutex
}

func (n *nativeWatcher) open(w *Watcher) error {
	n.w = w
	n.roots = make(map[string]*directoryWatch)
	return nil
}
func (n *nativeWatcher) add(name string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	for root := range n.roots {
		if name == root || strings.HasPrefix(name, root+string(filepath.Separator)) {
			return nil
		}
	}
	path, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(path, windows.FILE_LIST_DIRECTORY, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return err
	}
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		windows.CloseHandle(handle)
		return err
	}
	d := &directoryWatch{handle: handle, event: event, stopped: make(chan struct{})}
	// Arm the first read before Add returns, closing the initial-snapshot gap.
	buffer := new([notificationBufferSize]byte)
	overlapped := &windows.Overlapped{HEvent: event}
	if err = n.arm(d, buffer, overlapped); err != nil {
		windows.CloseHandle(event)
		windows.CloseHandle(handle)
		return err
	}
	n.roots[name] = d
	go n.read(name, d, buffer, overlapped)
	return nil
}
func (n *nativeWatcher) arm(d *directoryWatch, buffer *[notificationBufferSize]byte, overlapped *windows.Overlapped) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closing {
		return os.ErrClosed
	}
	windows.ResetEvent(d.event)
	const filter = windows.FILE_NOTIFY_CHANGE_FILE_NAME | windows.FILE_NOTIFY_CHANGE_DIR_NAME | windows.FILE_NOTIFY_CHANGE_LAST_WRITE | windows.FILE_NOTIFY_CHANGE_SIZE | windows.FILE_NOTIFY_CHANGE_ATTRIBUTES
	err := windows.ReadDirectoryChanges(d.handle, &buffer[0], uint32(len(buffer)), true, filter, nil, overlapped, 0)
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		return nil
	}
	return err
}
func (n *nativeWatcher) read(root string, d *directoryWatch, buffer *[notificationBufferSize]byte, overlapped *windows.Overlapped) {
	defer close(d.stopped)
	for {
		var count uint32
		err := windows.GetOverlappedResult(d.handle, overlapped, &count, true)
		if err != nil {
			if errors.Is(err, windows.ERROR_OPERATION_ABORTED) {
				return
			}
			if errors.Is(err, windows.ERROR_NOTIFY_ENUM_DIR) {
				n.w.fail(ErrOverflow)
			} else {
				if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
					n.w.emit(Event{Name: root, Op: Remove, Directory: true})
				} else {
					n.w.fail(err)
				}
				return
			}
		} else if count == 0 {
			n.w.fail(ErrOverflow)
		} else {
			n.decode(root, buffer[:count])
		}
		if err = n.arm(d, buffer, overlapped); err != nil {
			if !errors.Is(err, windows.ERROR_OPERATION_ABORTED) && !errors.Is(err, os.ErrClosed) {
				n.w.fail(err)
			}
			return
		}
	}
}
func (n *nativeWatcher) decode(root string, buffer []byte) {
	for offset := 0; offset+12 <= len(buffer); {
		raw := (*windows.FileNotifyInformation)(unsafe.Pointer(&buffer[offset]))
		end := offset + 12 + int(raw.FileNameLength)
		if raw.FileNameLength%2 != 0 || end > len(buffer) {
			n.w.fail(ErrOverflow)
			return
		}
		name := filepath.Join(root, windows.UTF16ToString(unsafe.Slice(&raw.FileName, int(raw.FileNameLength/2))))
		var op Op
		switch raw.Action {
		case windows.FILE_ACTION_ADDED, windows.FILE_ACTION_RENAMED_NEW_NAME:
			op = Create
		case windows.FILE_ACTION_REMOVED:
			op = Remove
		case windows.FILE_ACTION_RENAMED_OLD_NAME:
			op = Rename
		case windows.FILE_ACTION_MODIFIED:
			op = Write
		}
		if op != 0 {
			n.w.emit(Event{Name: name, Op: op})
		}
		if raw.NextEntryOffset == 0 {
			return
		}
		if raw.NextEntryOffset < 12 || int(raw.NextEntryOffset) > len(buffer)-offset {
			n.w.fail(ErrOverflow)
			return
		}
		offset += int(raw.NextEntryOffset)
	}
}
func stopDirectoryWatch(d *directoryWatch) {
	d.mu.Lock()
	d.closing = true
	_ = windows.CancelIoEx(d.handle, nil)
	d.mu.Unlock()
	<-d.stopped
	_ = windows.CloseHandle(d.handle)
	_ = windows.CloseHandle(d.event)
}

func (n *nativeWatcher) remove(name string) error {
	n.mu.Lock()
	d := n.roots[name]
	delete(n.roots, name)
	n.mu.Unlock()
	if d != nil {
		stopDirectoryWatch(d)
	}
	return nil
}
func (n *nativeWatcher) close() {
	n.mu.Lock()
	defer n.mu.Unlock()
	for root, d := range n.roots {
		stopDirectoryWatch(d)
		delete(n.roots, root)
	}
}
