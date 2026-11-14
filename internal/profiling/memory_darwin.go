package profiling

import (
	"fmt"
	"os"
	"sync"

	"github.com/ebitengine/purego"
	"golang.org/x/sys/unix"
)

var darwinMemory struct {
	rusage    func(int32, int32, *[12]uint64) int32
	hostStats func(uint32, int32, *[15]uint32, *uint32) int32
	err       error
	once      sync.Once
	host      uint32
}

func initializeDarwinMemory() {
	lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_LAZY|purego.RTLD_LOCAL)
	if err != nil {
		darwinMemory.err = err
		return
	}
	var hostSelf func() uint32
	purego.RegisterLibFunc(&hostSelf, lib, "mach_host_self")
	purego.RegisterLibFunc(&darwinMemory.rusage, lib, "proc_pid_rusage")
	purego.RegisterLibFunc(&darwinMemory.hostStats, lib, "host_statistics")
	darwinMemory.host = hostSelf()
}

func readMemory() (processMemory, systemMemory) {
	darwinMemory.once.Do(initializeDarwinMemory)
	var p processMemory
	var s systemMemory
	if darwinMemory.err != nil {
		p.Error, s.Error = darwinMemory.err.Error(), darwinMemory.err.Error()
		return p, s
	}
	// rusage_info_v0: 16-byte UUID followed by ten uint64 fields. Arrays keep
	// the native ABI layout independent of Go struct field alignment.
	var usage [12]uint64
	if darwinMemory.rusage(int32(os.Getpid()), 0, &usage) == 0 {
		p.CPUTimeNS = usage[2] + usage[3]
		p.ResidentBytes, p.FootprintBytes = usage[8], usage[9]
	} else {
		p.Error = "proc_pid_rusage failed"
	}
	total, err := unix.SysctlUint64("hw.memsize")
	// HOST_VM_INFO (2) uses the fixed 15-word vm_statistics ABI. On macOS
	// available is free + inactive pages, not Linux's MemAvailable estimate.
	var vm [15]uint32
	count := uint32(len(vm))
	if err == nil && darwinMemory.hostStats(darwinMemory.host, 2, &vm, &count) == 0 && count >= 4 {
		s.TotalBytes = total
		s.AvailableBytes = (uint64(vm[0]) + uint64(vm[2])) * uint64(os.Getpagesize())
	} else {
		if err == nil {
			err = fmt.Errorf("host_statistics failed")
		}
		s.Error = err.Error()
	}
	return p, s
}
