//go:build !tinygo && !lowmem

package bridge

import "fmt"

type statusPublicationCache struct{}

func (*statusPublicationCache) statusObjects(a *App) map[string]any {
	objects, err := a.freshStatusObjects()
	a.warnStatusSelection(err)
	return objects
}
func (*statusPublicationCache) klippyStatusTopic(a *App) string {
	return fmt.Sprintf("%s/klipper/state", a.config.MQTT.TopicPrefix)
}
func (*statusPublicationCache) objectStatusTopic(a *App, name string) string {
	return fmt.Sprintf("%s/objects/%s", a.config.MQTT.TopicPrefix, name)
}
