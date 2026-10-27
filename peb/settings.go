package peb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

type SettingType uint32

const (
	SettingString SettingType = iota
	SettingBoolean
	SettingNumber
	SettingInteger
)

// Setting IDs are local to an extension. Defaults are JSON scalar values;
// Options, when present, constrain a string setting to the listed values.
type Setting struct {
	ID, Label, Description string
	Type                   SettingType
	Default                json.RawMessage
	Options                []SettingOption
}

type SettingOption struct{ Value, Label string }
type SettingValues map[string]json.RawMessage

// DecodeSetting reads a typed value from a complete settings snapshot.
func DecodeSetting[T any](values SettingValues, id string) (value T, err error) {
	data, ok := values[id]
	if !ok {
		return value, fmt.Errorf("unknown setting %q", id)
	}
	err = json.Unmarshal(data, &value)
	return
}

func (s Setting) Validate(value json.RawMessage) error {
	if len(value) > MaxString || !utf8.Valid(value) {
		return fmt.Errorf("invalid value for setting %q", s.ID)
	}
	value = bytes.TrimSpace(value)
	valid := false
	switch s.Type {
	case SettingString:
		var v string
		valid = len(value) != 0 && value[0] == '"' && json.Unmarshal(value, &v) == nil
		if valid && len(s.Options) != 0 {
			valid = false
			for _, option := range s.Options {
				if v == option.Value {
					valid = true
					break
				}
			}
		}
	case SettingBoolean:
		valid = string(value) == "true" || string(value) == "false"
	case SettingNumber:
		var v float64
		valid = len(value) != 0 && (value[0] == '-' || value[0] >= '0' && value[0] <= '9') && json.Unmarshal(value, &v) == nil
	case SettingInteger:
		var v int64
		valid = len(value) != 0 && (value[0] == '-' || value[0] >= '0' && value[0] <= '9') && json.Unmarshal(value, &v) == nil
	}
	if !valid {
		return fmt.Errorf("invalid value for setting %q", s.ID)
	}
	return nil
}

func ValidateSettings(settings []Setting) error {
	if len(settings) > 256 {
		return fmt.Errorf("too many extension settings")
	}
	for i, s := range settings {
		if s.ID == "" || strings.ContainsAny(s.ID, "/\\\x00") || strings.TrimSpace(s.Label) == "" || s.Type > SettingInteger || len(s.Options) > 256 || len(s.Options) != 0 && s.Type != SettingString {
			return fmt.Errorf("invalid setting %q", s.ID)
		}
		for _, previous := range settings[:i] {
			if previous.ID == s.ID {
				return fmt.Errorf("duplicate setting %q", s.ID)
			}
		}
		for j, option := range s.Options {
			if option.Label == "" {
				return fmt.Errorf("missing option label for setting %q", s.ID)
			}
			for _, previous := range s.Options[:j] {
				if previous.Value == option.Value {
					return fmt.Errorf("duplicate option for setting %q", s.ID)
				}
			}
		}
		if err := s.Validate(s.Default); err != nil {
			return err
		}
	}
	return nil
}

// ResolveSettings creates an immutable snapshot with defaults for missing or
// invalid saved values. Unknown keys remain in storage, outside this snapshot.
func ResolveSettings(settings []Setting, saved SettingValues) SettingValues {
	if len(settings) == 0 {
		return nil
	}
	values := make(SettingValues, len(settings))
	for _, s := range settings {
		value := saved[s.ID]
		if s.Validate(value) != nil {
			value = s.Default
		}
		values[s.ID] = value
	}
	return values
}

func encodeSettings(e *encoder, settings []Setting) {
	e.count(len(settings))
	for _, s := range settings {
		e.str(s.ID)
		e.str(s.Label)
		e.str(s.Description)
		e.number(uint32(s.Type))
		e.str(string(s.Default))
		e.count(len(s.Options))
		for _, option := range s.Options {
			e.str(option.Value)
			e.str(option.Label)
		}
	}
}

func decodeSettings(d *decoder) []Setting {
	n := d.count()
	if n == 0 {
		return nil
	}
	if n > 256 {
		d.err = ErrProtocol
		return nil
	}
	settings := make([]Setting, n)
	for i := range settings {
		s := &settings[i]
		s.ID, s.Label, s.Description = d.str(), d.str(), d.str()
		s.Type, s.Default = SettingType(d.number()), json.RawMessage(d.str())
		if n := d.count(); n != 0 {
			if n > 256 {
				d.err = ErrProtocol
				return nil
			}
			s.Options = make([]SettingOption, n)
		}
		for j := range s.Options {
			s.Options[j] = SettingOption{d.str(), d.str()}
		}
	}
	if ValidateSettings(settings) != nil {
		d.err = ErrProtocol
	}
	return settings
}

// EncodeSettingValues reuses dst. Values must be validated against their schema
// before publication; the peer validates JSON when decoding the snapshot.
func EncodeSettingValues(dst []byte, values SettingValues) ([]byte, error) {
	e := encoder{data: dst[:0]}
	e.count(len(values))
	for id, value := range values {
		e.str(id)
		e.str(string(value))
	}
	return e.data, e.err
}

func DecodeSettingValues(data []byte) (SettingValues, error) {
	if len(data) > MaxPayload {
		return nil, ErrProtocol
	}
	d := decoder{data: data}
	n := d.count()
	values := make(SettingValues, n)
	for range n {
		id, value := d.str(), json.RawMessage(d.str())
		if _, exists := values[id]; exists || id == "" || !json.Valid(value) {
			d.err = ErrProtocol
		}
		values[id] = value
	}
	return values, d.done()
}
