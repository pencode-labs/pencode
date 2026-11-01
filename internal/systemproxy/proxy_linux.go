package systemproxy

import (
	"context"
	"net"
	"os/exec"
	"strings"
	"time"
)

func read() settings {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, "gsettings", "list-recursively", "org.gnome.system.proxy").Output()
	if err != nil {
		return settings{}
	}
	return gnomeSettings(string(data))
}

func gnomeSettings(data string) settings {
	values := make(map[string]string)
	for line := range strings.SplitSeq(data, "\n") {
		schema, rest, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		key, value, ok := strings.Cut(rest, " ")
		if ok {
			values[strings.TrimPrefix(schema, "org.gnome.system.proxy")+"/"+key] = strings.Trim(strings.TrimSpace(value), "'")
		}
	}
	if values["/mode"] != "manual" {
		return settings{}
	}
	address := func(protocol string) string {
		host, port := values["."+protocol+"/host"], values["."+protocol+"/port"]
		if host == "" || port == "" || port == "0" {
			return ""
		}
		return proxyURL(net.JoinHostPort(host, port))
	}
	s := settings{http: address("http"), https: address("https")}
	if values["/use-same-proxy"] == "true" {
		s.https = s.http
	}
	for host := range strings.SplitSeq(strings.Trim(values["/ignore-hosts"], "[]"), ",") {
		host = strings.Trim(strings.TrimSpace(host), "'\"")
		if host != "" {
			if s.bypass != "" {
				s.bypass += ","
			}
			s.bypass += host
		}
	}
	return s
}
