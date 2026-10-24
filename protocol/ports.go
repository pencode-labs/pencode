package protocol

import (
	"net/netip"
	"strings"
)

// Hosts are passed directly to the remote TCP dialer, never to a shell.
func ValidPortHost(host string) bool {
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	if strings.ContainsRune(host, ':') {
		_, err := netip.ParseAddr(host)
		return err == nil
	}
	for _, c := range host {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return host != "." && host != "-"
}
