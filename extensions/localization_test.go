package extensions

import (
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/pencode-labs/pencode/peb"
)

func TestLanguagePackResources(t *testing.T) {
	source := fstest.MapFS{"resources/locales/it.json": {Data: []byte(`{"id":"it","label":"Italiano","messages":{"设置":"Impostazioni"}}`)}}
	item := peb.LanguagePack{ID: "it", Label: "Italiano", Path: "resources/locales/it.json"}
	contributions := peb.Contributions{LanguagePacks: []peb.LanguagePack{item}}
	if got := contributionResources(contributions); !reflect.DeepEqual(got, []string{item.Path}) {
		t.Fatalf("resources: %v", got)
	}
	bundle, err := loadBundle(source, "italian", contributions)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.LanguagePacks) != 1 || bundle.LanguagePacks[0].Text("设置") != "Impostazioni" {
		t.Fatal("language pack not loaded")
	}
	for name, change := range map[string]func(*peb.LanguagePack){
		"escape":   func(p *peb.LanguagePack) { p.Path = "resources/../locales/it.json" },
		"outside":  func(p *peb.LanguagePack) { p.Path = "locales/it.json" },
		"missing":  func(p *peb.LanguagePack) { p.Path = "resources/missing.json" },
		"metadata": func(p *peb.LanguagePack) { p.ID = "fr" },
		"label":    func(p *peb.LanguagePack) { p.Label = "Other" },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := item
			change(&invalid)
			if _, err := loadBundle(source, "italian", peb.Contributions{LanguagePacks: []peb.LanguagePack{invalid}}); err == nil {
				t.Fatal("invalid resource accepted")
			}
		})
	}
	if _, err := loadBundle(source, "italian", peb.Contributions{LanguagePacks: []peb.LanguagePack{item, item}}); err == nil {
		t.Fatal("duplicate pack accepted")
	}
}
