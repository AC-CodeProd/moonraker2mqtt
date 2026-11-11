//go:build !tinygo && !lowmem

package bridge

import "fmt"

type statusPublicationCache struct{}

func (a *App) statusObjects() map[string]any {
	objects, err := a.freshStatusObjects()
	a.warnStatusSelection(err)
	return objects
}
func (a *App) klippyStatusTopic() string {
	return fmt.Sprintf("%s/klipper/state", a.config.MQTT.TopicPrefix)
}
func (a *App) objectStatusTopic(name string) string {
	return fmt.Sprintf("%s/objects/%s", a.config.MQTT.TopicPrefix, name)
}
