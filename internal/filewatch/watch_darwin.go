package filewatch

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"
)

const Recursive = true

type eventStreamContext struct {
	version                            int64
	info, retain, release, description uintptr
}
type nativeStream struct {
	stream, queue uintptr
	id            uintptr
}
type streamTarget struct {
	watcher         *Watcher
	root, canonical string
}
type nativeWatcher struct {
	w       *Watcher
	streams map[string]nativeStream
	mu      sync.Mutex
}

var fsevents struct {
	err                             error
	stop, invalidate, releaseStream func(uintptr)
	createArray                     func(uintptr, *uintptr, int64, uintptr) uintptr
	release                         func(uintptr)
	create                          func(uintptr, uintptr, *eventStreamContext, uintptr, uint64, float64, uint32) uintptr
	setQueue                        func(uintptr, uintptr)
	start                           func(uintptr) bool
	releaseQueue                    func(uintptr)
	createString                    func(uintptr, string, uint32) uintptr
	createQueue                     func(string, uintptr) uintptr
	syncQueue                       func(uintptr, uintptr, uintptr)
	targets                         sync.Map
	callback, barrier               uintptr
	next                            atomic.Uint64
	once                            sync.Once
}

func initializeFSEvents() {
	core, err := purego.Dlopen("/System/Library/Frameworks/CoreServices.framework/CoreServices", purego.RTLD_LAZY|purego.RTLD_LOCAL)
	if err != nil {
		fsevents.err = err
		return
	}
	foundation, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY|purego.RTLD_LOCAL)
	if err != nil {
		fsevents.err = err
		return
	}
	dispatch, err := purego.Dlopen("/usr/lib/system/libdispatch.dylib", purego.RTLD_LAZY|purego.RTLD_LOCAL)
	if err != nil {
		fsevents.err = err
		return
	}
	purego.RegisterLibFunc(&fsevents.createString, foundation, "CFStringCreateWithCString")
	purego.RegisterLibFunc(&fsevents.createArray, foundation, "CFArrayCreate")
	purego.RegisterLibFunc(&fsevents.release, foundation, "CFRelease")
	purego.RegisterLibFunc(&fsevents.create, core, "FSEventStreamCreate")
	purego.RegisterLibFunc(&fsevents.setQueue, core, "FSEventStreamSetDispatchQueue")
	purego.RegisterLibFunc(&fsevents.start, core, "FSEventStreamStart")
	purego.RegisterLibFunc(&fsevents.stop, core, "FSEventStreamStop")
	purego.RegisterLibFunc(&fsevents.invalidate, core, "FSEventStreamInvalidate")
	purego.RegisterLibFunc(&fsevents.releaseStream, core, "FSEventStreamRelease")
	purego.RegisterLibFunc(&fsevents.createQueue, dispatch, "dispatch_queue_create")
	purego.RegisterLibFunc(&fsevents.syncQueue, dispatch, "dispatch_sync_f")
	purego.RegisterLibFunc(&fsevents.releaseQueue, dispatch, "dispatch_release")
	fsevents.callback = purego.NewCallback(receiveFSEvents)
	fsevents.barrier = purego.NewCallback(func(uintptr) {})
}
func (n *nativeWatcher) open(w *Watcher) error {
	fsevents.once.Do(initializeFSEvents)
	if fsevents.err != nil {
		return fsevents.err
	}
	n.w = w
	n.streams = make(map[string]nativeStream)
	return nil
}
func (n *nativeWatcher) add(root string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	for path := range n.streams {
		if root == path || strings.HasPrefix(root, path+string(filepath.Separator)) {
			return nil
		}
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	str := fsevents.createString(0, canonical, 0x08000100)
	if str == 0 {
		return errors.New("FSEvents: cannot encode root")
	}
	defer fsevents.release(str)
	paths := fsevents.createArray(0, &str, 1, 0)
	if paths == 0 {
		return errors.New("FSEvents: cannot create root list")
	}
	defer fsevents.release(paths)
	id := uintptr(fsevents.next.Add(1))
	target := &streamTarget{watcher: n.w, root: root, canonical: canonical}
	fsevents.targets.Store(id, target)
	context := eventStreamContext{info: id}
	// NoDefer | WatchRoot | FileEvents. All names enter the common file service.
	stream := fsevents.create(0, fsevents.callback, &context, paths, ^uint64(0), 0.05, 0x02|0x04|0x10)
	runtime.KeepAlive(context)
	if stream == 0 {
		fsevents.targets.Delete(id)
		return errors.New("FSEvents: cannot create stream")
	}
	queue := fsevents.createQueue("dev.pencode.files", 0)
	if queue == 0 {
		fsevents.releaseStream(stream)
		fsevents.targets.Delete(id)
		return errors.New("FSEvents: cannot create queue")
	}
	fsevents.setQueue(stream, queue)
	if !fsevents.start(stream) {
		fsevents.invalidate(stream)
		fsevents.releaseStream(stream)
		fsevents.releaseQueue(queue)
		fsevents.targets.Delete(id)
		return errors.New("FSEvents: cannot start stream")
	}
	n.streams[root] = nativeStream{stream: stream, queue: queue, id: id}
	return nil
}
func stopStream(stream nativeStream) {
	fsevents.stop(stream.stream)
	fsevents.invalidate(stream.stream)
	// Drain callbacks before deleting their target or releasing the queue.
	fsevents.syncQueue(stream.queue, 0, fsevents.barrier)
	fsevents.releaseStream(stream.stream)
	fsevents.releaseQueue(stream.queue)
	fsevents.targets.Delete(stream.id)
}
func (n *nativeWatcher) remove(root string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if stream, ok := n.streams[root]; ok {
		stopStream(stream)
		delete(n.streams, root)
	}
	return nil
}
func (n *nativeWatcher) close() {
	n.mu.Lock()
	defer n.mu.Unlock()
	for root, stream := range n.streams {
		stopStream(stream)
		delete(n.streams, root)
	}
}
func receiveFSEvents(_ uintptr, id uintptr, count uintptr, paths uintptr, flags uintptr, _ uintptr) {
	value, ok := fsevents.targets.Load(id)
	if !ok {
		return
	}
	target := value.(*streamTarget)
	names := unsafe.Slice((**byte)(unsafe.Pointer(paths)), int(count))
	masks := unsafe.Slice((*uint32)(unsafe.Pointer(flags)), int(count))
	for i, ptr := range names {
		mask := masks[i]
		// MustScanSubDirs, UserDropped, KernelDropped, EventIdsWrapped or unmount.
		if mask&(0x01|0x02|0x04|0x08|0x80) != 0 {
			target.watcher.fail(ErrOverflow)
			continue
		}
		length := 0
		for *(*byte)(unsafe.Add(unsafe.Pointer(ptr), length)) != 0 {
			length++
		}
		path := unsafe.String(ptr, length)
		if path == target.canonical {
			path = target.root
		} else if strings.HasPrefix(path, target.canonical+"/") {
			path = target.root + strings.TrimPrefix(path, target.canonical)
		} else {
			continue
		}
		var op Op
		if mask&0x100 != 0 {
			op |= Create
		}
		if mask&0x200 != 0 {
			op |= Remove
		}
		if mask&0x1000 != 0 {
			op |= Write
		}
		if mask&(0x400|0x2000|0x4000|0x8000) != 0 {
			op |= Chmod
		}
		if mask&0x800 != 0 {
			if _, err := os.Lstat(path); err == nil {
				op |= Create
			} else {
				op |= Rename
			}
		}
		if mask&0x20 != 0 {
			path = target.root
			op = Rename
		}
		if op != 0 {
			target.watcher.emit(Event{Name: strings.Clone(path), Op: op, Directory: mask&0x20000 != 0})
		}
	}
}
