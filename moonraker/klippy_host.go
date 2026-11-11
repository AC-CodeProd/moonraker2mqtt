//go:build !tinygo && !lowmem

package moonraker

import "context"

func (c *Client) getKlippyState(ctx context.Context) (string, error) {
	info, err := c.GetServerInfo(ctx)
	if err != nil {
		return "", err
	}
	return info.KlippyState, nil
}
