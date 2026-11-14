package profiling

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var kernelMemory = windows.NewLazySystemDLL("kernel32.dll")
var processMemoryInfo = kernelMemory.NewProc("K32GetProcessMemoryInfo")
var globalMemoryStatus = kernelMemory.NewProc("GlobalMemoryStatusEx")

func readMemory() (processMemory, systemMemory) {
	var p processMemory
	var s systemMemory
	// PROCESS_MEMORY_COUNTERS_EX: two DWORDs, then nine SIZE_T fields.
	var counters struct {
		Size, PageFaultCount uint32
		Values               [9]uintptr
	}
	counters.Size = uint32(unsafe.Sizeof(counters))
	process := windows.CurrentProcess()
	ok, _, err := processMemoryInfo.Call(uintptr(process), uintptr(unsafe.Pointer(&counters)), uintptr(counters.Size))
	if ok != 0 {
		p.ResidentBytes = uint64(counters.Values[1])
		p.PrivateBytes = uint64(counters.Values[8])
	} else {
		p.Error = err.Error()
	}
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(process, &created, &exited, &kernel, &user); err != nil {
		p.Error = err.Error()
	} else {
		p.CPUTimeNS = ((uint64(kernel.HighDateTime)<<32 | uint64(kernel.LowDateTime)) +
			(uint64(user.HighDateTime)<<32 | uint64(user.LowDateTime))) * 100
	}
	// MEMORYSTATUSEX: length/load DWORD pair followed by seven uint64s.
	status := [8]uint64{64}
	ok, _, err = globalMemoryStatus.Call(uintptr(unsafe.Pointer(&status)))
	if ok != 0 {
		s.TotalBytes, s.AvailableBytes = status[1], status[2]
	} else {
		s.Error = err.Error()
	}
	return p, s
}
