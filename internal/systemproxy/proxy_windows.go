package systemproxy

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

func read() settings {
	root := registry.CURRENT_USER
	if policy, err := registry.OpenKey(registry.LOCAL_MACHINE, `Software\Policies\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE); err == nil {
		perUser, _, err := policy.GetIntegerValue("ProxySettingsPerUser")
		policy.Close()
		if err == nil && perUser == 0 {
			root = registry.LOCAL_MACHINE
		}
	}
	key, err := registry.OpenKey(root, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return settings{}
	}
	defer key.Close()
	enabled, _, err := key.GetIntegerValue("ProxyEnable")
	if err != nil || enabled == 0 {
		return settings{}
	}
	server, _, _ := key.GetStringValue("ProxyServer")
	bypass, _, _ := key.GetStringValue("ProxyOverride")
	return windowsSettings(server, bypass)
}

func windowsSettings(server, bypass string) settings {
	var s settings
	if !strings.Contains(server, "=") {
		s.http = proxyURL(server)
		s.https = s.http
	} else {
		for entry := range strings.SplitSeq(server, ";") {
			protocol, address, ok := strings.Cut(strings.TrimSpace(entry), "=")
			if !ok {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(protocol)) {
			case "http":
				s.http = proxyURL(address)
			case "https":
				// This is the destination protocol; the proxy still uses HTTP.
				s.https = proxyURL(address)
			}
		}
	}
	for host := range strings.SplitSeq(bypass, ";") {
		host = strings.TrimSpace(host)
		if strings.EqualFold(host, "<local>") {
			s.local = true
		} else if host != "" {
			if s.bypass != "" {
				s.bypass += ","
			}
			s.bypass += host
		}
	}
	return s
}
