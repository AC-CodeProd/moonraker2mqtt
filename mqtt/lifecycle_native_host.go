//go:build natiu && !tinygo

package mqtt

// Hosted local adapter tests deliberately use the injectable net.Conn dial.
// This is not native storage qualification; native owner tests are separate.
func newMQTTNativeOwner() (mqttNativeOwner, error) { return nil, nil }
