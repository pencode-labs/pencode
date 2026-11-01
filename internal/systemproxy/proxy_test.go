package systemproxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"testing"
)

func cleanEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range [...]string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy", "REQUEST_METHOD"} {
		t.Setenv(key, "")
	}
	transport := http.DefaultTransport.(*http.Transport)
	original := transport.Proxy
	t.Cleanup(func() { transport.Proxy = original })
}

func TestConfigurePreservesExplicitProxy(t *testing.T) {
	for _, key := range [...]string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy"} {
		t.Run(key, func(t *testing.T) {
			cleanEnvironment(t)
			t.Setenv(key, "http://explicit.test:8080")
			Configure()
			if !explicitProxy() || os.Getenv(key) != "http://explicit.test:8080" || os.Getenv("NO_PROXY") != "" {
				t.Fatal("explicit configuration changed")
			}
		})
	}
}

func TestSystemProxyRoutesRequests(t *testing.T) {
	// net/http caches proxy environment settings for the process lifetime.
	if os.Getenv("PENCODE_TEST_PROXY_ROUTES") != "1" {
		t.Setenv("PENCODE_TEST_PROXY_ROUTES", "1")
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), executable, "-test.run=^TestSystemProxyRoutesRequests$")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("routing test failed: %v\n%s", err, output)
		}
		return
	}
	cleanEnvironment(t)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.String() != "http://outside.invalid/resource" {
			t.Errorf("proxy received %s", r.URL)
		}
		_, _ = io.WriteString(w, "through system proxy")
	}))
	defer proxy.Close()
	apply(settings{http: proxy.URL, https: proxy.URL, bypass: "*.internal.test,10.0.0.0/8", local: true})
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	request, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://outside.invalid/resource", nil)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "through system proxy" {
		t.Fatalf("response = %q, %v", body, err)
	}
	for _, address := range []string{"https://outside.invalid", "http://service.internal.test", "http://10.2.3.4", "http://localhost", "http://127.0.0.1", "http://[::1]", "http://intranet:8080"} {
		u, _ := url.Parse(address)
		got, err := transport.Proxy(&http.Request{URL: u})
		want := ""
		if address == "https://outside.invalid" {
			want = proxy.URL
		}
		if err != nil || got != nil && got.String() != want || got == nil && want != "" {
			t.Errorf("proxy for %s = %v, %v; want %q", address, got, err, want)
		}
	}
	local, _ := url.Parse("http://intranet:8080")
	r := &http.Request{URL: local}
	if allocations := testing.AllocsPerRun(100, func() { _, _ = transport.Proxy(r) }); allocations != 0 {
		t.Fatalf("local bypass allocated %g times", allocations)
	}
}

func TestSystemProxyPreservesExplicitBypass(t *testing.T) {
	for _, key := range []string{"NO_PROXY", "no_proxy"} {
		t.Run(key, func(t *testing.T) {
			cleanEnvironment(t)
			t.Setenv(key, "custom.test")
			apply(settings{http: "http://proxy.test:8080", bypass: "system.test", local: true})
			if os.Getenv(key) != "custom.test" {
				t.Fatal("explicit bypass overwritten")
			}
		})
	}
}

func TestAllProxyFallback(t *testing.T) {
	cleanEnvironment(t)
	t.Setenv("ALL_PROXY", "socks5://proxy.test:1080")
	t.Setenv("HTTP_PROXY", "http://override.test:8080")
	Configure()
	if os.Getenv("HTTP_PROXY") != "http://override.test:8080" || os.Getenv("HTTPS_PROXY") != "socks5://proxy.test:1080" {
		t.Fatal("ALL_PROXY fallback lost protocol-specific override")
	}
}

func TestSystemProxyChildEnvironment(t *testing.T) {
	if os.Getenv("PENCODE_TEST_PROXY_CHILD") == "1" {
		if os.Getenv("HTTP_PROXY") != "http://proxy.test:8080" || os.Getenv("HTTPS_PROXY") != "http://secure.test:8081" || os.Getenv("NO_PROXY") != "internal.test" {
			t.Fatal("child did not inherit proxy settings")
		}
		return
	}
	cleanEnvironment(t)
	apply(settings{http: "http://proxy.test:8080", https: "http://secure.test:8081", bypass: "internal.test"})
	t.Setenv("PENCODE_TEST_PROXY_CHILD", "1")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), executable, "-test.run=^TestSystemProxyChildEnvironment$")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child failed: %v\n%s", err, output)
	}
}

func TestProxyURL(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{" 127.0.0.1:7890 ", "http://127.0.0.1:7890"},
		{"[::1]:7890", "http://[::1]:7890"},
		{"http://proxy.test:8080", "http://proxy.test:8080"},
		{"socks5://proxy.test:1080", "socks5://proxy.test:1080"},
		{"", ""}, {"http://", ""}, {"invalid proxy", ""}, {"file:///proxy", ""},
	} {
		if got := proxyURL(test.input); got != test.want {
			t.Errorf("proxyURL(%q) = %q; want %q", test.input, got, test.want)
		}
	}
}
