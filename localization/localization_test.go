package localization

import (
	"regexp"
	"slices"
	"testing"
)

func TestBuiltinPacksAndFallback(t *testing.T) {
	packs := Builtins()
	if len(packs) != 7 {
		t.Fatalf("builtin packs: %d", len(packs))
	}
	ids := make(map[string]bool)
	for _, pack := range packs {
		if ids[pack.ID] {
			t.Fatalf("duplicate %s", pack.ID)
		}
		ids[pack.ID] = true
		if pack.Text("设置") == "" {
			t.Fatal("empty settings label")
		}
		if pack.Text("unknown message") != "unknown message" {
			t.Fatal("unknown source changed")
		}
		if pack.ID != Default && pack.Text("设置") == "设置" {
			t.Fatalf("untranslated settings: %s", pack.ID)
		}
	}
	custom, err := Parse([]byte(`{"id":"it","label":"Italiano","messages":{"设置":"Impostazioni"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if custom.Text("设置") != "Impostazioni" || custom.Text("保存") != "Save" {
		t.Fatal("translation or English fallback lost")
	}
	if got := (Pack{ID: Default}).Text("保存"); got != "保存" {
		t.Fatalf("Chinese fallback: %q", got)
	}
	if n := testing.AllocsPerRun(1000, func() { custom.Text("设置"); custom.Text("保存"); custom.Text("unknown message") }); n != 0 {
		t.Fatalf("lookup allocated %g times", n)
	}
	// Every built-in translation must preserve its formatting arguments.
	placeholders := regexp.MustCompile(`%[+#.0-9\-]*[a-zA-Z]`)
	for _, pack := range packs {
		for source, value := range pack.Messages {
			before, after := placeholders.FindAllString(source, -1), placeholders.FindAllString(value, -1)
			slices.Sort(before)
			slices.Sort(after)
			if !slices.Equal(before, after) {
				t.Errorf("%s: format changed: %q -> %q", pack.ID, source, value)
			}
		}
	}
}

func TestRejectInvalidPacks(t *testing.T) {
	for _, data := range []string{
		`{}`, `{"id":"../it","label":"Italiano"}`, `{"id":"it","label":" "}`,
		`{"id":"it","label":"Italiano","messages":{"":"empty"}}`,
		`{"id":"it","label":"Italiano","messages":{"设置":""}}`,
		`{"id":"it","label":"Italiano","messages":{"设置":42}}`,
	} {
		t.Run(data, func(t *testing.T) {
			if _, err := Parse([]byte(data)); err == nil {
				t.Fatal("invalid pack accepted")
			}
		})
	}
}
