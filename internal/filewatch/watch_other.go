//go:build !linux && !windows && !darwin

package filewatch

import "errors"

const Recursive = true

type nativeWatcher struct{}

func (*nativeWatcher) open(*Watcher) error {
	return errors.New("native file events require macOS, Windows or Linux")
}
func (*nativeWatcher) add(string) error    { return nil }
func (*nativeWatcher) remove(string) error { return nil }
func (*nativeWatcher) close()              {}
