//go:build jsonborrowtest && natiu && !tinygo

package mqtt

import (
	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
	"net"
)

// NotificationFixtureClient is available only in the explicit offline test tag.
// It exposes injection of a fake transport, not an alternate Publish path.
func NotificationFixtureClient(cfg config.MQTTConfig, log logger.Logger, conn net.Conn) *NatiuClient {
	c := NewNatiuClient(cfg, log)
	c.dial = func(string, string) (net.Conn, error) { return conn, nil }
	return c
}
