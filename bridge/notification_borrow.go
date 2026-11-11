//go:build tinygo || jsonborrowtest

package bridge

import "moonraker2mqtt/mqtt"

// Only the audited concrete MCU adapter is qualified. Wrappers and unknown
// asynchronous implementations keep ordinary owned Marshal semantics.
func notificationBorrowQualified(c mqtt.MQTTClient) bool { _, ok := c.(*mqtt.NatiuClient); return ok }
