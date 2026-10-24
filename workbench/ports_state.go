package workbench

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/pencode-labs/pencode/protocol"
)

const pencodePortLimit = 128

type pencodePortSpec struct {
	Host          string
	Port          uint16
	Local         uint16
	Label         string
	Protocol      string
	AllInterfaces bool
	Auto          bool
}

type pencodePortsState struct {
	Forwards   []pencodePortSpec
	Suppressed []string
}

func normalizePortHost(host string) string {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "0.0.0.0", "::1", "::":
		return "localhost"
	default:
		return strings.ToLower(host)
	}
}

func (s pencodePortSpec) key() string {
	return net.JoinHostPort(normalizePortHost(s.Host), strconv.Itoa(int(s.Port)))
}

func parsePortAddress(value string) (protocol.Port, error) {
	value = strings.TrimSpace(value)
	host, number := "localhost", value
	if strings.ContainsRune(value, ':') {
		var err error
		host, number, err = net.SplitHostPort(value)
		if err != nil {
			return protocol.Port{}, fmt.Errorf("请输入端口号或主机:端口，例如 3000、localhost:3000、[::1]:3000")
		}
	}
	n, err := strconv.ParseUint(number, 10, 16)
	if err != nil || n == 0 || !protocol.ValidPortHost(host) {
		return protocol.Port{}, fmt.Errorf("端口必须在 1–65535 之间，主机名不能为空")
	}
	return protocol.Port{Number: uint16(n), Host: normalizePortHost(host)}, nil
}
