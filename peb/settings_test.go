package peb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func settingsFixture() []Setting {
	return []Setting{
		{ID: "name", Label: "名称", Description: "显示的名称", Default: json.RawMessage(`"Pencode"`)},
		{ID: "enabled", Label: "启用", Type: SettingBoolean, Default: json.RawMessage(`true`)},
		{ID: "scale", Label: "比例", Type: SettingNumber, Default: json.RawMessage(`1.5`)},
		{ID: "limit", Label: "数量", Type: SettingInteger, Default: json.RawMessage(`10`)},
		{ID: "mode", Label: "模式", Default: json.RawMessage(`"auto"`), Options: []SettingOption{{"auto", "自动"}, {"manual", "手动"}}},
	}
}

func TestSettingsSchemaRoundTripAndValidation(t *testing.T) {
	want := Contributions{Settings: settingsFixture()}
	data, err := EncodeContributions(nil, want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeContributions(data)
	if err != nil || !reflect.DeepEqual(got.Settings, want.Settings) {
		t.Fatalf("round trip: %+v, %v", got, err)
	}
	for n := range len(data) {
		if _, err := DecodeContributions(data[:n]); err == nil {
			t.Fatalf("accepted truncated schema at %d", n)
		}
	}
	clear(data)
	if !reflect.DeepEqual(got.Settings, want.Settings) {
		t.Fatal("schema aliases borrowed payload")
	}
	for _, modify := range []func([]Setting){
		func(s []Setting) { s[0].ID = "" },
		func(s []Setting) { s[0].ID = s[1].ID },
		func(s []Setting) { s[0].ID = "a/b" },
		func(s []Setting) { s[0].Label = "" },
		func(s []Setting) { s[0].Type = 99 },
		func(s []Setting) { s[0].Default = json.RawMessage(`null`) },
		func(s []Setting) { s[1].Default = json.RawMessage(`"true"`) },
		func(s []Setting) { s[2].Default = json.RawMessage(`1e999`) },
		func(s []Setting) { s[3].Default = json.RawMessage(`1.5`) },
		func(s []Setting) { s[4].Default = json.RawMessage(`"unknown"`) },
		func(s []Setting) { s[4].Options[1].Value = "auto" },
		func(s []Setting) { s[4].Options[1].Label = "" },
		func(s []Setting) { s[4].Type = SettingBoolean },
	} {
		s := settingsFixture()
		modify(s)
		if err := ValidateSettings(s); err == nil {
			t.Fatalf("accepted invalid schema: %+v", s)
		}
		// Exercise decoder validation independently of the guarded encoder.
		e := encoder{}
		encodeSettings(&e, s)
		d := decoder{data: e.data}
		decodeSettings(&d)
		if d.done() == nil {
			t.Fatalf("decoded invalid schema: %+v", s)
		}
	}
}

func TestSettingsValuesDefaultsAndCodec(t *testing.T) {
	saved := SettingValues{"name": json.RawMessage(`"你好"`), "enabled": json.RawMessage(`null`), "mode": json.RawMessage(`"removed"`), "unknown": json.RawMessage(`123`)}
	values := ResolveSettings(settingsFixture(), saved)
	if name, err := DecodeSetting[string](values, "name"); err != nil || name != "你好" {
		t.Fatal(name, err)
	}
	if enabled, err := DecodeSetting[bool](values, "enabled"); err != nil || !enabled {
		t.Fatal(enabled, err)
	}
	if mode, err := DecodeSetting[string](values, "mode"); err != nil || mode != "auto" {
		t.Fatal(mode, err)
	}
	if _, err := DecodeSetting[string](values, "missing"); err == nil {
		t.Fatal("missing setting had no error")
	}
	if len(values) != 5 || len(saved) != 4 {
		t.Fatal("resolution changed saved settings or retained unknown keys")
	}
	data, err := EncodeSettingValues(nil, values)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeSettingValues(data)
	if err != nil || !reflect.DeepEqual(got, values) {
		t.Fatal(got, err)
	}
	for n := range len(data) {
		if _, err := DecodeSettingValues(data[:n]); err == nil {
			t.Fatalf("accepted truncated values at %d", n)
		}
	}
	if allocations := testing.AllocsPerRun(100, func() {
		data, err = EncodeSettingValues(data, values)
	}); allocations != 0 || err != nil {
		t.Fatalf("warm settings encoding: allocations=%g err=%v", allocations, err)
	}
}

func TestServeSettingsBeforeActivationAndLiveChanges(t *testing.T) {
	var input, output bytes.Buffer
	w := Writer{Target: &input}
	hello, _ := EncodeHello(nil, Hello{"pencode", Capabilities})
	_ = w.Write(Initialize, 1, hello)
	values := ResolveSettings(settingsFixture(), nil)
	data, _ := EncodeSettingValues(nil, values)
	_ = w.Write(SettingsRequest, 2, data)
	_ = w.Write(ActionActivate, 0, nil)
	values["name"] = json.RawMessage(`"new"`)
	data, _ = EncodeSettingValues(data, values)
	_ = w.Write(SettingsRequest, 3, data)
	_ = w.Write(Shutdown, 0, nil)
	var names []string
	active := false
	err := ServeExtension(&input, &output, Contributions{Settings: settingsFixture()}, Handlers{
		Settings: func(values SettingValues) error {
			name, err := DecodeSetting[string](values, "name")
			names = append(names, name)
			if len(names) == 1 && active || len(names) == 2 && !active {
				t.Fatal("settings delivered in the wrong lifecycle phase")
			}
			return err
		},
		ActivateActions: func(_ context.Context, _ *ActionPublisher) { active = true },
	})
	if err != nil || !reflect.DeepEqual(names, []string{"Pencode", "new"}) {
		t.Fatal(names, err)
	}
	r := Reader{Source: &output}
	for i, kind := range []Kind{Register, SettingsResponse, SettingsResponse} {
		f, err := r.Read()
		if err != nil || f.Kind != kind || f.ID != uint32(i+1) {
			t.Fatal(f, err)
		}
	}
	if err := w.Write(SettingsResponse, 0, nil); !errors.Is(err, ErrProtocol) {
		t.Fatal("accepted response with no request ID")
	}
}
