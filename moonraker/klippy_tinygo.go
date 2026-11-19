//go:build tinygo || lowmem

package moonraker

import "context"

func (c *Client) getKlippyState(ctx context.Context) (string, error) {
	if typed, ok := c.wsClient.(interface {
		RequestKlippyState(context.Context) (string, error)
	}); ok {
		return typed.RequestKlippyState(ctx)
	}
	// Compatibility with alternate adapters/mocks: do not change Client interface.
	info, err := c.GetServerInfo(ctx)
	if err != nil {
		return "", err
	}
	return info.KlippyState, nil
}
