//go:build !linux

package server

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/pencode-labs/pencode/protocol"
)

func listeningPorts(ctx context.Context) ([]protocol.Port, error) {
	if runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("此系统暂不支持自动检测端口，请手动添加转发")
	}
	data, err := exec.CommandContext(ctx, "lsof", "-nP", "-a", "-u", strconv.Itoa(os.Getuid()), "-iTCP", "-sTCP:LISTEN", "-Fpcn").Output()
	if err != nil && len(data) == 0 {
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
			return nil, err
		}
	}
	var ports []protocol.Port
	var pid uint32
	var process string
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.HasPrefix(line, "p") {
			number, _ := strconv.ParseUint(line[1:], 10, 32)
			pid, process = uint32(number), ""
			continue
		}
		if strings.HasPrefix(line, "c") {
			process = line[1:]
			continue
		}
		if !strings.HasPrefix(line, "n") {
			continue
		}
		address := line[1:]
		i := strings.LastIndexByte(address, ':')
		if i < 0 {
			continue
		}
		number, err := strconv.ParseUint(address[i+1:], 10, 16)
		if err != nil || number == 0 {
			continue
		}
		host := "127.0.0.1"
		switch address[:i] {
		case "*", "127.0.0.1":
		case "[::1]", "[::]":
			host = "::1"
		default:
			host = strings.Trim(address[:i], "[]")
			if !protocol.ValidPortHost(host) {
				continue
			}
		}
		port := protocol.Port{Number: uint16(number), Host: host, PID: pid, Process: process}
		if !slices.Contains(ports, port) {
			ports = append(ports, port)
		}
	}
	return ports, nil
}
