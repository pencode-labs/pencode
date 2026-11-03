package extensions

import (
	"sync"

	"github.com/pencode-labs/pencode/peb"
)

const fileChangeCapacity = 128

// Persistent processes each own a bounded mailbox. A busy extension merges
// paths until it can read them; overflow requests reconciliation instead.
type fileChangeMailbox struct {
	mu    sync.Mutex
	ready chan struct{}
	roots map[string]*pendingFileChanges
	spare *pendingFileChanges
}
type pendingFileChanges struct {
	files [fileChangeCapacity]peb.FileEvent
	count int
	reset bool
}

func (m *fileChangeMailbox) initialize() {
	m.ready = make(chan struct{}, 1)
	m.roots = make(map[string]*pendingFileChanges)
}
func (m *fileChangeMailbox) submit(changes peb.FileChanges) {
	if changes.Root == "" || !changes.Reset && len(changes.Files) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.roots == nil {
		return
	}
	pending := m.roots[changes.Root]
	if pending == nil {
		pending = m.spare
		m.spare = nil
		if pending == nil {
			pending = &pendingFileChanges{}
		}
		m.roots[changes.Root] = pending
	}
	pending.reset = pending.reset || changes.Reset
	for _, file := range changes.Files {
		found := false
		for i := range pending.count {
			if pending.files[i].Path == file.Path {
				pending.files[i].Op |= file.Op
				pending.files[i].Directory = pending.files[i].Directory || file.Directory
				found = true
				break
			}
		}
		if found {
			continue
		}
		if pending.count == len(pending.files) {
			pending.reset = true
			continue
		}
		pending.files[pending.count] = file
		pending.count++
	}
	select {
	case m.ready <- struct{}{}:
	default:
	}
}
func (m *fileChangeMailbox) take() peb.FileChanges {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result peb.FileChanges
	for root, pending := range m.roots {
		if pending.count == 0 && !pending.reset {
			continue
		}
		result.Root, result.Reset = root, pending.reset
		result.Files = append(result.Files, pending.files[:pending.count]...)
		clear(pending.files[:pending.count])
		pending.count = 0
		pending.reset = false
		delete(m.roots, root)
		m.spare = pending
		break
	}
	for _, pending := range m.roots {
		if pending.count != 0 || pending.reset {
			select {
			case m.ready <- struct{}{}:
			default:
			}
			break
		}
	}
	return result
}
func (m *fileChangeMailbox) stop() {
	m.mu.Lock()
	m.roots, m.spare = nil, nil
	m.mu.Unlock()
}
func (s *ActionSession) NotifyFiles(changes peb.FileChanges) { s.files.submit(changes) }
func (s *EditorSession) NotifyFiles(changes peb.FileChanges) { s.files.submit(changes) }
