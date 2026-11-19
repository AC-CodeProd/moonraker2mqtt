package bridge

import "moonraker2mqtt/internal/diagjson"

// Each queried object owns its encoded bytes through synchronous Publish.
// Release drops only our reference, including on error or panic unwinding.
func (a *App) publishStatusObject(objectName string, objectData any) bool {
	var publication diagjson.Notification
	defer publication.Release() // before user encoding hooks, not deferred in the loop
	if err := publication.EncodeStatus(objectData, notificationBorrowQualified(a.mqttClient)); err != nil {
		a.logger.Error("Failed to marshal object %s: %v", objectName, err)
		return false
	}
	topic := a.objectStatusTopic(objectName)
	if err := a.mqttClient.Publish(topic, publication.Bytes(), a.config.MQTT.QoS, false, 3); err != nil {
		a.logger.Error("Failed to publish object %s after retries: %v", objectName, err)
		return false
	}
	return true
}
