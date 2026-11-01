package systemproxy

import "testing"

func TestDarwinSettings(t *testing.T) {
	data := `<dictionary> {
  ExceptionsList : <array> {
    0 : *.local
    1 : 169.254.0.0/16
  }
  ExcludeSimpleHostnames : 1
  HTTPEnable : 1
  HTTPPort : 8080
  HTTPProxy : proxy.test
  HTTPSEnable : 1
  HTTPSPort : 8081
  HTTPSProxy : secure.test
  __SCOPED__ : <dictionary> {
    en0 : <dictionary> {
      HTTPProxy : scoped.test
    }
  }
}`
	want := settings{http: "http://proxy.test:8080", https: "http://secure.test:8081", bypass: "*.local,169.254.0.0/16", local: true}
	if got := darwinSettings(data); got != want {
		t.Fatalf("settings = %+v; want %+v", got, want)
	}
	if got := darwinSettings("<dictionary> {\nHTTPEnable : 0\nHTTPPort : 8080\nHTTPProxy : proxy.test\n}"); got != (settings{}) {
		t.Fatalf("disabled proxy = %+v", got)
	}
}
