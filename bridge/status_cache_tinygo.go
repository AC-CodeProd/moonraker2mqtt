//go:build tinygo || lowmem

package bridge

// Runner-owned, like publishStatus itself: no concurrent publication or config
// mutation is supported. Lazily snapshot stable firmware configuration on the
// first publication (also supports Apps assembled directly by offline tests).
// Parsed maps/slices own their data. QueryObjects must treat them as read-only.
type statusPublicationCache struct {
	prepared     bool
	objects      map[string]any
	selectionErr error
	klippyTopic  string
	objectPrefix string
	topics       map[string]string
}

func (a *App) prepareStatusCache() {
	c := &a.statusCache
	if c.prepared {
		return
	}
	c.objects, c.selectionErr = a.freshStatusObjects()
	c.klippyTopic = a.config.MQTT.TopicPrefix + "/klipper/state"
	c.objectPrefix = a.config.MQTT.TopicPrefix + "/objects/"
	c.topics = make(map[string]string, len(c.objects)+1)
	for name := range c.objects {
		c.topics[name] = c.objectPrefix + name
	}
	// QueryObjects currently returns the outer result, not flattened objects.
	c.topics["status"] = c.objectPrefix + "status"
	c.prepared = true
}
func (a *App) statusObjects() map[string]any {
	a.prepareStatusCache()
	a.warnStatusSelection(a.statusCache.selectionErr)
	return a.statusCache.objects
}
func (a *App) klippyStatusTopic() string { a.prepareStatusCache(); return a.statusCache.klippyTopic }
func (a *App) objectStatusTopic(name string) string {
	a.prepareStatusCache()
	if topic, ok := a.statusCache.topics[name]; ok {
		return topic
	}
	// Preserve unexpected outer keys without an unbounded response-driven cache.
	return a.statusCache.objectPrefix + name
}
