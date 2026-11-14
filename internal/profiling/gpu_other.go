//go:build !linux

package profiling

func readDRMMemory() drmMemory { return drmMemory{} }
