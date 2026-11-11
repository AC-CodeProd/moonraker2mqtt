//go:build tinygo

package mqtt

import "net"

// Each MQTT client gets its own durable native reserve, independent of MR.
func newMQTTNativeOwner() (mqttNativeOwner, error) { return net.NewOwnedDialer() }
