package extensions

import (
	"errors"

	"github.com/pencode-labs/pencode/peb"
)

// A single foreground producer replaces waiting snapshots; workers never see
// mutations to a published map. Idle sessions do no settings work.
func submitSettings(queue chan peb.SettingValues, done <-chan struct{}, values peb.SettingValues) bool {
	select {
	case <-done:
		return false
	default:
	}
	select {
	case <-queue:
	default:
	}
	select {
	case queue <- values:
		return true
	default:
		return false
	}
}

func exchangeSettings(r *peb.Reader, w *peb.Writer, data []byte, values peb.SettingValues, id uint32) ([]byte, error) {
	if values == nil {
		return data, nil
	}
	data, err := peb.EncodeSettingValues(data, values)
	if err != nil {
		return data, err
	}
	if err := w.Write(peb.SettingsRequest, id, data); err != nil {
		return data, err
	}
	f, err := r.Read()
	if err != nil {
		return data, err
	}
	if f.ID != id {
		return data, peb.ErrProtocol
	}
	if f.Kind == peb.Failure {
		message, err := peb.DecodeFailure(f.Payload)
		if err != nil {
			return data, err
		}
		return data, errors.New(message)
	}
	if f.Kind != peb.SettingsResponse {
		return data, peb.ErrProtocol
	}
	return data, nil
}
