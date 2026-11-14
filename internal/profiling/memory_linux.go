package profiling

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func readMemory() (processMemory, systemMemory) {
	var p processMemory
	var s systemMemory
	data, err := os.ReadFile("/proc/self/statm")
	if err == nil {
		fields := strings.Fields(string(data))
		if len(fields) < 2 {
			err = fmt.Errorf("invalid /proc/self/statm")
		} else {
			p.ResidentBytes, err = strconv.ParseUint(fields[1], 10, 64)
			p.ResidentBytes *= uint64(os.Getpagesize())
		}
	}
	var usage unix.Rusage
	if err == nil {
		err = unix.Getrusage(unix.RUSAGE_SELF, &usage)
		p.CPUTimeNS = uint64(usage.Utime.Nano() + usage.Stime.Nano())
	}
	if err != nil {
		p.Error = err.Error()
	}
	data, err = os.ReadFile("/proc/meminfo")
	if err == nil {
		available := false
		for line := range strings.SplitSeq(string(data), "\n") {
			key, value, ok := strings.Cut(line, ":")
			if !ok || key != "MemTotal" && key != "MemAvailable" {
				continue
			}
			bytes, parseErr := parseBytes(value)
			if parseErr != nil {
				err = parseErr
				break
			}
			if key == "MemTotal" {
				s.TotalBytes = bytes
			} else {
				s.AvailableBytes = bytes
				available = true
			}
		}
		if s.TotalBytes == 0 || !available {
			err = fmt.Errorf("missing MemTotal or MemAvailable in /proc/meminfo")
		}
	}
	if err != nil {
		s.Error = err.Error()
	}
	return p, s
}

func parseBytes(value string) (uint64, error) {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return 0, fmt.Errorf("missing memory value")
	}
	n, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return 0, err
	}
	if len(fields) > 1 {
		switch fields[1] {
		case "kB", "KiB":
			n *= 1024
		case "MiB":
			n *= 1024 * 1024
		default:
			return 0, fmt.Errorf("unknown memory unit %q", fields[1])
		}
	}
	return n, nil
}

// DRM clients can have duplicated descriptors. Deduplicate by device and client
// ID, as specified by the kernel fdinfo ABI. Count only resident regions, never
// requested (drm-total-*) storage or upload traffic.
func readDRMMemory() drmMemory {
	var result drmMemory
	entries, err := os.ReadDir("/proc/self/fdinfo")
	if err != nil {
		result.Error = err.Error()
		return result
	}
	seen := make(map[string]bool)
	for _, entry := range entries {
		data, err := os.ReadFile("/proc/self/fdinfo/" + entry.Name())
		if err != nil { // Descriptors can close during sampling.
			continue
		}
		device, client, resident, available, err := parseDRM(string(data))
		if err != nil {
			result.Error = err.Error()
			result.Available = false
			return result
		}
		if !available || client == "" {
			continue
		}
		key := device + "/" + client
		if seen[key] {
			continue
		}
		seen[key] = true
		result.Available = true
		result.ResidentBytes += resident
	}
	return result
}

func parseDRM(data string) (device, client string, resident uint64, available bool, err error) {
	for line := range strings.SplitSeq(data, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch key {
		case "drm-pdev":
			device = value
		case "drm-client-id":
			client = value
		default:
			if strings.HasPrefix(key, "drm-resident-") {
				var bytes uint64
				bytes, err = parseBytes(value)
				if err != nil {
					return
				}
				resident += bytes
				available = true
			}
		}
	}
	return
}
