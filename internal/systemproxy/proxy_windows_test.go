package systemproxy

import "testing"

func TestWindowsSettings(t *testing.T) {
	for _, test := range []struct {
		name, server, bypass string
		want                 settings
	}{
		{"shared", "127.0.0.1:7890", "localhost; *.internal.test ;<LOCAL>;10.0.0.0/8", settings{
			http: "http://127.0.0.1:7890", https: "http://127.0.0.1:7890", bypass: "localhost,*.internal.test,10.0.0.0/8", local: true,
		}},
		{"per protocol", "http=proxy.test:8080; https = secure.test:8081;ftp=other.test:21", "", settings{
			http: "http://proxy.test:8080", https: "http://secure.test:8081",
		}},
		{"https only", "https=127.0.0.1:7890", "", settings{https: "http://127.0.0.1:7890"}},
		{"invalid", "invalid proxy", "", settings{}},
		{"empty", "", "", settings{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := windowsSettings(test.server, test.bypass); got != test.want {
				t.Fatalf("settings = %+v; want %+v", got, test.want)
			}
		})
	}
}
