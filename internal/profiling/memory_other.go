//go:build !linux && !darwin && !windows

package profiling

func readMemory() (processMemory, systemMemory) {
	return processMemory{Error: "process memory unavailable on this platform"}, systemMemory{Error: "system memory unavailable on this platform"}
}
