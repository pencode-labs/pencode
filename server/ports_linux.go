package server

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/pencode-labs/pencode/protocol"
)

func listeningPorts(ctx context.Context) ([]protocol.Port, error) {
	var ports []protocol.Port
	inodes := make(map[string][]int)
	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := os.Open(table)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			if err = ctx.Err(); err != nil {
				break
			}
			fields := strings.Fields(scanner.Text())
			if len(fields) < 10 || fields[3] != "0A" {
				continue
			}
			uid, parseErr := strconv.Atoi(fields[7])
			if parseErr != nil || uid != os.Getuid() {
				continue
			}
			address, number, ok := strings.Cut(fields[1], ":")
			if !ok {
				continue
			}
			p, parseErr := strconv.ParseUint(number, 16, 16)
			if parseErr != nil || p == 0 {
				continue
			}
			host, ok := listeningHost(address)
			if !ok {
				continue
			}
			port := protocol.Port{Number: uint16(p), Host: host}
			if !slices.Contains(ports, port) {
				inodes[fields[9]] = append(inodes[fields[9]], len(ports))
				ports = append(ports, port)
			}
		}
		if err == nil {
			err = scanner.Err()
		}
		f.Close()
		if err != nil {
			return nil, err
		}
	}
	processes, err := os.ReadDir("/proc")
	if err != nil {
		return ports, nil
	}
	for _, process := range processes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid, err := strconv.ParseUint(process.Name(), 10, 32)
		if err != nil {
			continue
		}
		directory := filepath.Join("/proc", process.Name())
		fds, err := os.ReadDir(filepath.Join(directory, "fd"))
		if err != nil {
			continue
		}
		var description string
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(directory, "fd", fd.Name()))
			if err != nil || !strings.HasPrefix(link, "socket:[") {
				continue
			}
			indices := inodes[strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")]
			if len(indices) == 0 {
				continue
			}
			if description == "" {
				file, err := os.Open(filepath.Join(directory, "cmdline"))
				if err == nil {
					var command [4096]byte
					n, _ := file.Read(command[:])
					file.Close()
					description = strings.TrimSpace(strings.ReplaceAll(string(command[:n]), "\x00", " "))
				}
			}
			for _, index := range indices {
				ports[index].PID, ports[index].Process = uint32(pid), description
			}
		}
	}
	return ports, nil
}

func listeningHost(encoded string) (string, bool) {
	if len(encoded) != 8 && len(encoded) != 32 {
		return "", false
	}
	var raw [16]byte
	for i := 0; i < len(encoded); i += 8 {
		word, err := strconv.ParseUint(encoded[i:i+8], 16, 32)
		if err != nil {
			return "", false
		}
		binary.NativeEndian.PutUint32(raw[i/2:i/2+4], uint32(word))
	}
	address, _ := netip.AddrFromSlice(raw[:len(encoded)/2])
	if address.IsUnspecified() {
		if address.Is4() {
			return "127.0.0.1", true
		}
		return "::1", true
	}
	return address.String(), true
}
