//go:build !darwin

package desktopopen

// Configure is only needed on macOS, where Finder sends document events rather
// than command-line arguments. Windows and Linux pass paths to the executable.
func Configure(func(string)) {}
