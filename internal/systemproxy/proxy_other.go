//go:build !windows && !darwin && !linux

package systemproxy

func read() settings { return settings{} }
