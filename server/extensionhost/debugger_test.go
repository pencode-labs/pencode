package extensionhost

import (
	"testing"

	"github.com/pencode-labs/pencode/peb"
)

func TestDebugAdapterTemplatesValidation(t *testing.T) {
	for _, tc := range []struct {
		template string
		valid    bool
	}{
		{"", true},
		{`[]`, true},
		{`[{"label":"Go","adapter":"Delve","request":"launch","configuration":{"program":"${workspaceFolder}"}}]`, true},
		{`{`, false},
		{`null`, false},
		{`[{"label":"Go","adapter":"Other","request":"launch","configuration":{}}]`, false},
		{`[{"label":"Go","adapter":"Delve","request":"invalid","configuration":{}}]`, false},
	} {
		err := validateDebugAdapters([]peb.DebugAdapter{{ID: "Delve", Label: "Go", Command: "dlv", Templates: tc.template}})
		if (err == nil) != tc.valid {
			t.Errorf("%q: %v", tc.template, err)
		}
	}
}
