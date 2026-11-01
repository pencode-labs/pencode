package systemproxy

import (
	"strings"
	"testing"
)

func TestGnomeSettings(t *testing.T) {
	data := `org.gnome.system.proxy mode 'manual'
org.gnome.system.proxy use-same-proxy false
org.gnome.system.proxy ignore-hosts ['localhost', '127.0.0.0/8', '*.internal.test']
org.gnome.system.proxy.http host 'proxy.test'
org.gnome.system.proxy.http port 8080
org.gnome.system.proxy.https host 'secure.test'
org.gnome.system.proxy.https port 8081
`
	want := settings{http: "http://proxy.test:8080", https: "http://secure.test:8081", bypass: "localhost,127.0.0.0/8,*.internal.test"}
	if got := gnomeSettings(data); got != want {
		t.Fatalf("settings = %+v; want %+v", got, want)
	}
	want.https = want.http
	if got := gnomeSettings(strings.Replace(data, "use-same-proxy false", "use-same-proxy true", 1)); got != want {
		t.Fatalf("shared proxy = %+v; want %+v", got, want)
	}
	for _, mode := range []string{"none", "auto"} {
		if got := gnomeSettings(strings.Replace(data, "'manual'", "'"+mode+"'", 1)); got != (settings{}) {
			t.Fatalf("%s proxy = %+v", mode, got)
		}
	}
}
