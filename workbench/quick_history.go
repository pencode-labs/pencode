package workbench

import (
	"encoding/binary"
	"os"
	"path/filepath"
)

const (
	pencodeQuickHistoryLimit  = 128
	pencodeQuickHistoryHeader = "pencode-quick-open\x01"
)

type pencodeQuickHistoryEntry struct {
	project, path string
}

type pencodeQuickHistory struct {
	file    string
	entries [pencodeQuickHistoryLimit]pencodeQuickHistoryEntry
	count   int
	ranks   map[pencodeQuickHistoryEntry]int
	buffer  []byte
	dirty   bool
}

func loadPencodeQuickHistory(directory string) *pencodeQuickHistory {
	history := &pencodeQuickHistory{ranks: make(map[pencodeQuickHistoryEntry]int, pencodeQuickHistoryLimit)}
	if directory == "" {
		return history
	}
	history.file = filepath.Join(directory, "quick-open.history")
	data, err := os.ReadFile(history.file)
	if err != nil || len(data) < len(pencodeQuickHistoryHeader) || string(data[:len(pencodeQuickHistoryHeader)]) != pencodeQuickHistoryHeader {
		return history
	}
	history.buffer = data[:0]
	data = data[len(pencodeQuickHistoryHeader):]
	for len(data) > 0 {
		var entry pencodeQuickHistoryEntry
		for _, value := range [...]*string{&entry.project, &entry.path} {
			length, size := binary.Uvarint(data)
			if size <= 0 || length == 0 || length > uint64(len(data)-size) {
				history.reset()
				return history
			}
			*value = string(data[size : size+int(length)])
			data = data[size+int(length):]
		}
		if _, exists := history.ranks[entry]; exists || history.count == len(history.entries) {
			history.reset()
			return history
		}
		history.ranks[entry] = history.count
		history.entries[history.count] = entry
		history.count++
	}
	return history
}

func (history *pencodeQuickHistory) reset() {
	clear(history.entries[:])
	clear(history.ranks)
	history.count = 0
}

func (history *pencodeQuickHistory) rank(project, path string) int {
	if history != nil {
		if rank, ok := history.ranks[pencodeQuickHistoryEntry{project, path}]; ok {
			return rank
		}
	}
	return pencodeQuickHistoryLimit
}

func (history *pencodeQuickHistory) remember(project, path string) error {
	entry := pencodeQuickHistoryEntry{project, path}
	index, exists := history.ranks[entry]
	if exists && index == 0 && !history.dirty {
		return nil
	}
	if !exists {
		index = min(history.count, len(history.entries)-1)
		if history.count == len(history.entries) {
			delete(history.ranks, history.entries[index])
		} else {
			history.count++
		}
	}
	for ; index > 0; index-- {
		previous := history.entries[index-1]
		history.entries[index] = previous
		history.ranks[previous] = index
	}
	history.entries[0], history.ranks[entry] = entry, 0
	history.dirty = history.file != ""
	if history.file == "" {
		return nil
	}
	history.buffer = append(history.buffer[:0], pencodeQuickHistoryHeader...)
	for _, entry := range history.entries[:history.count] {
		for _, value := range [...]string{entry.project, entry.path} {
			history.buffer = binary.AppendUvarint(history.buffer, uint64(len(value)))
			history.buffer = append(history.buffer, value...)
		}
	}
	directory := filepath.Dir(history.file)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".quick-open-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(history.buffer)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	err = os.Rename(file.Name(), history.file)
	history.dirty = err != nil
	return err
}
