package client

import (
	"context"
	"io/fs"

	"github.com/pencode-labs/pencode/configuration"
	"github.com/pencode-labs/pencode/protocol"
)

func (c *Client) ReadConfiguration(ctx context.Context) (configuration.Snapshot, error) {
	e := service(protocol.ConfigurationRead, "")
	d, err := c.execute(ctx, &e)
	if err != nil {
		return configuration.Snapshot{}, err
	}
	s := configuration.Snapshot{Directory: d.String()}
	count := d.U32()
	if count > configuration.MaxFiles || int(count) > len(d.Buffer)/8 {
		return s, protocol.ErrProtocol
	}
	s.Files = make(map[string][]byte, count)
	total := 0
	for range count {
		name, data := d.String(), d.Bytes()
		total += len(data)
		if !fs.ValidPath(name) || total > configuration.MaxBytes {
			return s, protocol.ErrProtocol
		}
		if _, exists := s.Files[name]; exists {
			return s, protocol.ErrProtocol
		}
		s.Files[name] = data
	}
	return s, d.Done()
}

func (c *Client) SaveSettings(ctx context.Context, data []byte) error {
	e := service(protocol.SettingsSave, "")
	e.Bytes(data)
	d, err := c.execute(ctx, &e)
	if err != nil {
		return err
	}
	return d.Done()
}
