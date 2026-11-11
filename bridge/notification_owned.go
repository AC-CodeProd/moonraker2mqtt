//go:build !tinygo && !jsonborrowtest

package bridge

import "moonraker2mqtt/mqtt"

func notificationBorrowQualified(mqtt.MQTTClient) bool { return false }
