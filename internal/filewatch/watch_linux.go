package filewatch

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

const Recursive = false

type nativeWatcher struct {
	w           *Watcher
	file        *os.File
	mu          sync.Mutex
	paths       map[int32]string
	descriptors map[string]int32
	stopped     chan struct{}
}

func (n *nativeWatcher) open(w *Watcher) error {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return err
	}
	n.w, n.file, n.paths, n.descriptors, n.stopped = w, os.NewFile(uintptr(fd), "inotify"), make(map[int32]string), make(map[string]int32), make(chan struct{})
	go n.read()
	return nil
}
func (n *nativeWatcher) add(name string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.descriptors[name]; ok {
		return nil
	}
	wd, err := unix.InotifyAddWatch(int(n.file.Fd()), name, unix.IN_CREATE|unix.IN_CLOSE_WRITE|unix.IN_MODIFY|unix.IN_DELETE|unix.IN_MOVED_FROM|unix.IN_MOVED_TO|unix.IN_DELETE_SELF|unix.IN_MOVE_SELF|unix.IN_ATTRIB|unix.IN_ONLYDIR)
	if err != nil {
		return err
	}
	n.paths[int32(wd)], n.descriptors[name] = name, int32(wd)
	return nil
}
func (n *nativeWatcher) remove(name string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	wd, ok := n.descriptors[name]
	if !ok {
		return nil
	}
	delete(n.descriptors, name)
	delete(n.paths, wd)
	_, err := unix.InotifyRmWatch(int(n.file.Fd()), uint32(wd))
	if errors.Is(err, unix.EINVAL) {
		return nil
	}
	return err
}
func (n *nativeWatcher) close() { _ = n.file.Close(); <-n.stopped }
func (n *nativeWatcher) read() {
	defer close(n.stopped)
	var buffer [64 << 10]byte
	for {
		count, err := n.file.Read(buffer[:])
		if err != nil {
			select {
			case <-n.w.done:
				return
			default:
				n.w.fail(err)
				return
			}
		}
		for offset := 0; offset+unix.SizeofInotifyEvent <= count; {
			raw := (*unix.InotifyEvent)(unsafe.Pointer(&buffer[offset]))
			end := offset + unix.SizeofInotifyEvent + int(raw.Len)
			if end > count {
				n.w.fail(ErrOverflow)
				break
			}
			if raw.Mask&unix.IN_Q_OVERFLOW != 0 {
				n.w.fail(ErrOverflow)
				offset = end
				continue
			}
			n.mu.Lock()
			parent := n.paths[raw.Wd]
			if raw.Mask&unix.IN_IGNORED != 0 {
				delete(n.paths, raw.Wd)
				delete(n.descriptors, parent)
			}
			n.mu.Unlock()
			if parent != "" && raw.Mask&unix.IN_IGNORED == 0 {
				name := parent
				if raw.Len != 0 {
					child := strings.TrimRight(string(buffer[offset+unix.SizeofInotifyEvent:end]), "\x00")
					name = filepath.Join(parent, child)
				}
				var op Op
				if raw.Mask&(unix.IN_CREATE|unix.IN_MOVED_TO) != 0 {
					op |= Create
				}
				if raw.Mask&(unix.IN_MODIFY|unix.IN_CLOSE_WRITE) != 0 {
					op |= Write
				}
				if raw.Mask&(unix.IN_DELETE|unix.IN_DELETE_SELF) != 0 {
					op |= Remove
				}
				if raw.Mask&(unix.IN_MOVED_FROM|unix.IN_MOVE_SELF) != 0 {
					op |= Rename
				}
				if raw.Mask&unix.IN_ATTRIB != 0 {
					op |= Chmod
				}
				if op != 0 {
					n.w.emit(Event{Name: name, Op: op, Directory: raw.Mask&unix.IN_ISDIR != 0 || raw.Len == 0})
				}
			}
			offset = end
		}
	}
}
