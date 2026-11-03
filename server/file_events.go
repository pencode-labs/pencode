package server

import (
	"sync"

	"github.com/pencode-labs/pencode/protocol"
)

const fileEventHistory = 512
const allWorkspaceChanges = protocol.WorkspaceTreeChanged | protocol.WorkspaceGitChanged | protocol.WorkspaceRepositoriesChanged

type fileEventRecord struct {
	version uint64
	changes protocol.WorkspaceChange
	file    protocol.FileEvent
	reset   bool
}

// One bounded journal serves independent editor/extension subscribers. Slow
// readers receive an explicit reset rather than silently losing notifications.
type fileEventJournal struct {
	mu            sync.Mutex
	root          string
	base, version uint64
	records       [fileEventHistory]fileEventRecord
	changed       chan struct{}
}

func (j *fileEventJournal) append(r fileEventRecord) {
	j.version++
	r.version = j.version
	j.records[j.version%fileEventHistory] = r
}
func (j *fileEventJournal) publish(changes protocol.WorkspaceChange, files []protocol.FileEvent, reset bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.append(fileEventRecord{changes: changes, reset: reset})
	for _, file := range files {
		j.append(fileEventRecord{file: file})
	}
	close(j.changed)
	j.changed = make(chan struct{})
}

// Caller holds mu. Encoding borrows retained paths and does not copy a batch.
func (j *fileEventJournal) encode(after uint64, e *protocol.Encoder) bool {
	if j.version == j.base || after == j.version {
		return false
	}
	reset := after <= j.base || after > j.version || j.version-after >= fileEventHistory
	var changes protocol.WorkspaceChange
	count := uint32(0)
	start := after + 1
	if reset {
		changes = allWorkspaceChanges
		start = j.version + 1
	}
	for version := start; version <= j.version; version++ {
		record := &j.records[version%fileEventHistory]
		changes |= record.changes
		reset = reset || record.reset
		if record.file.Path != "" {
			count++
		}
	}
	if reset {
		changes |= allWorkspaceChanges
	}
	e.U64(j.version)
	e.U32(uint32(changes))
	e.U32(boolNumber(reset))
	e.U32(count)
	for version := start; version <= j.version; version++ {
		file := &j.records[version%fileEventHistory].file
		if file.Path != "" {
			e.String(file.Path)
			e.U32(uint32(file.Op))
			e.U32(boolNumber(file.Directory))
		}
	}
	return true
}
func boolNumber(v bool) uint32 {
	if v {
		return 1
	}
	return 0
}
