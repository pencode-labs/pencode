package peb

import "testing"

func TestShortcutValidationAndProtocol(t *testing.T) {
	for _, chord := range []string{"Primary+Shift+B", "Ctrl+Space", "Super+Alt+/", "Shift+Enter", "F12", "Escape"} {
		parsed, err := ParseShortcut(chord)
		if err != nil || parsed.Key == "" {
			t.Fatalf("%q: %+v %v", chord, parsed, err)
		}
		a := Action{ID: "build", Text: "Build", Location: Command, Shortcut: chord}
		data, err := EncodeContributions(nil, Contributions{Actions: []Action{a}})
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeContributions(data)
		if err != nil || len(got.Actions) != 1 || !a.Equal(got.Actions[0]) {
			t.Fatalf("round trip %q: %+v %v", chord, got, err)
		}
		if allocations := testing.AllocsPerRun(100, func() { _, _ = ParseShortcut(chord) }); allocations != 0 {
			t.Fatalf("parse allocated %g", allocations)
		}
	}
	for _, chord := range []string{"", "Primary", "Ctrl+Ctrl+A", "Ctrl++A", "Ctrl+a", "Ctrl+F26", "A+B", "Ctrl+A+Shift", "Ctrl+A Ctrl+B"} {
		if _, err := ParseShortcut(chord); err == nil {
			t.Fatalf("accepted %q", chord)
		}
		if chord != "" {
			if _, err := EncodeActionChange(nil, ActionChange{Action: Action{Shortcut: chord}}); err == nil {
				t.Fatalf("encoded invalid shortcut %q", chord)
			}
		}
	}
	a := Action{ID: "build", Shortcut: "F6"}
	b := a
	b.Shortcut = "F7"
	if a.Equal(b) {
		t.Fatal("shortcut change did not invalidate action")
	}
}
