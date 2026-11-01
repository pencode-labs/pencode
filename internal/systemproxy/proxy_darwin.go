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
	data, err := exec.CommandContext(ctx, "/usr/sbin/scutil", "--proxy").Output()
	if err != nil {
		return settings{}
	}
	return darwinSettings(string(data))
}

func darwinSettings(data string) settings {
	values := make(map[string]string)
	var s settings
	depth, exceptions := 0, false
	for line := range strings.SplitSeq(data, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, "{") {
			depth++
			exceptions = depth == 2 && strings.HasPrefix(line, "ExceptionsList :")
			continue
		}
		if line == "}" {
			depth--
			exceptions = false
			continue
		}
		key, value, ok := strings.Cut(line, " : ")
		if !ok {
			continue
		}
		if depth == 1 {
			values[key] = value
		} else if exceptions {
			if s.bypass != "" {
				s.bypass += ","
			}
			s.bypass += value
		}
	}
	if values["HTTPEnable"] == "1" && values["HTTPProxy"] != "" && values["HTTPPort"] != "" {
		s.http = proxyURL(net.JoinHostPort(values["HTTPProxy"], values["HTTPPort"]))
	}
	if values["HTTPSEnable"] == "1" && values["HTTPSProxy"] != "" && values["HTTPSPort"] != "" {
		s.https = proxyURL(net.JoinHostPort(values["HTTPSProxy"], values["HTTPSPort"]))
	}
	s.local = values["ExcludeSimpleHostnames"] == "1"
	return s
}
