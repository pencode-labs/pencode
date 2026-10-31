package lsp

import (
	"bytes"
	"encoding/json"
	"errors"
	"sync"
)

const maxRetainedMessage = 1 << 20

// The writer owns each encoded message until it finishes writing or discards
// the queue. Large full-document messages are not retained in the pool.
var messageBuffers = sync.Pool{New: func() any { return new(bytes.Buffer) }}

func releaseMessage(data *bytes.Buffer) {
	if data != nil && data.Cap() <= maxRetainedMessage {
		data.Reset()
		messageBuffers.Put(data)
	}
}

func encodeMessage[T any](message T) (*bytes.Buffer, error) {
	data := messageBuffers.Get().(*bytes.Buffer)
	if err := json.NewEncoder(data).Encode(message); err != nil {
		releaseMessage(data)
		return nil, err
	}
	data.Truncate(data.Len() - 1) // Framing supplies the message boundary.
	if data.Len() > MaxMessage {
		releaseMessage(data)
		return nil, errors.New("lsp: message too large")
	}
	return data, nil
}

type requestMessage[P any] struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  P               `json:"params,omitempty"`
}
