//go:build tinygo

package mqtt

import "moonraker2mqtt/internal/nativetcp"

// Each MQTT client gets its own durable native reserve, independent of MR.
func newMQTTNativeOwner() (mqttNativeOwner, error) { return nativetcp.New() }
