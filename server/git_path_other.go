//go:build !darwin && !linux

package server

func gitExecutable() string { return "git" }
