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
	Label         string
	Protocol      string
	Port          uint16
	Local         uint16
	AllInterfaces bool
	Auto          bool
}

type pencodePortsState struct {
	Forwards   []pencodePortSpec
	Suppressed []pencodePortKey
}

func normalizePortHost(host string) string {
	host = strings.ToLower(host)
	switch host {
	case "localhost", "127.0.0.1", "0.0.0.0", "::1", "::":
		return "localhost"
	default:
		return host
	}
}

type pencodePortKey struct {
	host string
	port uint16
}

func (s pencodePortSpec) key() pencodePortKey {
	return pencodePortKey{normalizePortHost(s.Host), s.Port}
}

func (k pencodePortKey) String() string {
	return net.JoinHostPort(k.host, strconv.Itoa(int(k.port)))
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
