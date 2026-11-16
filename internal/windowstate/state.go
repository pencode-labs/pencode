// Package windowstate keeps crash recovery records while a window owns its lease.
package windowstate

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Window[T any] struct {
	Value    T
	lease    *os.File
	file     string
	Restored bool
}

// Open restores all windows on a cold application launch. A live lease means
// the application is already running; another launch then opens a fresh window.
// Explicit restore IDs identify additional windows of a cold launch.
func Open[T any](directory, id string, restore bool) (*Window[T], []string, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, nil, err
	}
	registry, err := lock(filepath.Join(directory, "registry.lock"), true)
	if err != nil {
		return nil, nil, err
	}
	defer registry.Close()
	if id != "" {
		if filepath.Base(id) != id || strings.ContainsAny(id, `/\\.`) {
			return nil, nil, fmt.Errorf("invalid window ID")
		}
		w, err := read[T](directory, id)
		return w, nil, err
	}
	var recovered []*Window[T]
	defer func() {
		for _, w := range recovered {
			w.Release()
		}
	}()
	if restore {
		entries, err := os.ReadDir(directory)
		if err != nil {
			return nil, nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			w, err := read[T](directory, strings.TrimSuffix(entry.Name(), ".json"))
			if errors.Is(err, errLocked) {
				restore = false
				break
			}
			if err != nil {
				var syntax *json.SyntaxError
				var value *json.UnmarshalTypeError
				if errors.As(err, &syntax) || errors.As(err, &value) || errors.Is(err, os.ErrNotExist) {
					continue // One damaged record must not block the other windows.
				}
				return nil, nil, err
			}
			recovered = append(recovered, w)
		}
	}
	if restore && len(recovered) != 0 {
		w := recovered[0]
		recovered = recovered[1:]
		ids := make([]string, 0, len(recovered))
		for _, other := range recovered {
			ids = append(ids, strings.TrimSuffix(filepath.Base(other.file), ".json"))
		}
		return w, ids, nil
	}
	id = rand.Text()
	lease, err := lock(filepath.Join(directory, id+".lock"), false)
	if err != nil {
		return nil, nil, err
	}
	w := &Window[T]{lease: lease, file: filepath.Join(directory, id+".json")}
	if err := w.Save(&w.Value); err != nil {
		w.Release()
		return nil, nil, err
	}
	return w, nil, nil
}

func read[T any](directory, id string) (*Window[T], error) {
	lease, err := lock(filepath.Join(directory, id+".lock"), false)
	if err != nil {
		return nil, err
	}
	w := &Window[T]{lease: lease, file: filepath.Join(directory, id+".json"), Restored: true}
	data, err := os.ReadFile(w.file)
	if err == nil {
		err = json.Unmarshal(data, &w.Value)
	}
	if err != nil {
		w.Release()
		return nil, fmt.Errorf("read window state %s: %w", id, err)
	}
	return w, nil
}

func (w *Window[T]) Save(value *T) error { return Write(w.file, value) }

// Forget is called only after the user's close request has been approved.
func (w *Window[T]) Forget() error {
	err := os.Remove(w.file)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Release leaves the recovery record intact on errors and process shutdown.
func (w *Window[T]) Release() {
	if w != nil && w.lease != nil {
		_ = w.lease.Close()
		w.lease = nil
	}
}

// Write atomically replaces a private state file, flushing before the rename so
// an interrupted write leaves the last complete snapshot available.
func Write[T any](name string, value *T) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	directory := filepath.Dir(name)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), name)
}
