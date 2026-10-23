package peb

import (
	"bytes"
	"reflect"
	"testing"
)

func TestLanguageServerContributionRoundTripAndCapabilities(t *testing.T) {
	want := Contributions{LanguageServers: []LanguageServer{{ID: "go", Label: "Go", LanguageID: "go", Command: "bin/gopls", Args: []string{"serve"}, Selectors: []DocumentSelector{{Match: FileSuffix, Pattern: ".go"}}, InitializationOptions: `{"中文":"😀"}`, Configuration: `{"gopls":{"staticcheck":true}}`}}}
	data, err := EncodeContributions(nil, want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeContributions(data)
	if err != nil || !reflect.DeepEqual(got.LanguageServers, want.LanguageServers) {
		t.Fatalf("%+v %v", got, err)
	}
	clear(data)
	if !reflect.DeepEqual(got.LanguageServers, want.LanguageServers) {
		t.Fatal("language server strings alias the reader")
	}
	for _, caps := range []Capability{Capabilities, Capabilities &^ LanguageServers} {
		var input, output bytes.Buffer
		w := Writer{Target: &input}
		hello, _ := EncodeHello(nil, Hello{Host: "test", Capabilities: caps})
		_ = w.Write(Initialize, 1, hello)
		_ = w.Write(Shutdown, 0, nil)
		if err := Serve(&input, &output, want); err != nil {
			t.Fatal(err)
		}
		r := Reader{Source: &output}
		frame, err := r.Read()
		if err != nil {
			t.Fatal(err)
		}
		if caps&LanguageServers != 0 && frame.Kind != Register || caps&LanguageServers == 0 && frame.Kind != Failure {
			t.Fatal("language server capability was ignored")
		}
	}
}
