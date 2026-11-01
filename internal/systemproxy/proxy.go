// Package systemproxy imports desktop proxy settings before networking or child
// processes start. Settings are read once, with no system queries per request.
package systemproxy

import (
	"net/http"
	"net/url"
	"os"
	"strings"
)

type settings struct {
	http, https, bypass string
	local               bool
}

// Configure preserves explicit proxy environment settings. Otherwise it exports
// the desktop's manual proxies so extensions and tools inherit the same settings.
// Call it during startup, before net/http caches the environment.
func Configure() {
	if explicitProxy() {
		// net/http does not read ALL_PROXY; normalize it for Go clients while
		// keeping any protocol-specific overrides.
		all := os.Getenv("all_proxy")
		if all == "" {
			all = os.Getenv("ALL_PROXY")
		}
		if all != "" {
			for _, keys := range [][2]string{{"HTTP_PROXY", "http_proxy"}, {"HTTPS_PROXY", "https_proxy"}} {
				if os.Getenv(keys[0]) == "" && os.Getenv(keys[1]) == "" {
					_ = os.Setenv(keys[0], all)
				}
			}
		}
		return
	}
	apply(read())
}

func explicitProxy() bool {
	for _, key := range [...]string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy"} {
		if os.Getenv(key) != "" {
			return true
		}
	}
	return false
}

func apply(s settings) {
	if s.http == "" && s.https == "" {
		return
	}
	if s.http != "" {
		_ = os.Setenv("HTTP_PROXY", s.http)
	}
	if s.https != "" {
		_ = os.Setenv("HTTPS_PROXY", s.https)
	}
	if os.Getenv("NO_PROXY") != "" || os.Getenv("no_proxy") != "" {
		return
	}
	if s.bypass != "" {
		_ = os.Setenv("NO_PROXY", s.bypass)
	}
	if s.local {
		// NO_PROXY has no syntax for unqualified intranet names (<local>).
		// Keep this rule for editor HTTP and WebSocket requests as well.
		http.DefaultTransport.(*http.Transport).Proxy = func(r *http.Request) (*url.URL, error) {
			if host := r.URL.Hostname(); !strings.ContainsAny(host, ".:") {
				return nil, nil
			}
			return http.ProxyFromEnvironment(r)
		}
	}
}

func proxyURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
		return value
	}
	return ""
}
