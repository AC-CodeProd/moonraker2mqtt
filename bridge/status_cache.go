package bridge

// Keep App's publication API stable while the platform-specific cache owns its
// behavior: fresh host configuration or a reusable firmware snapshot.
func (a *App) statusObjects() map[string]any {
	return a.statusCache.statusObjects(a)
}

func (a *App) klippyStatusTopic() string {
	return a.statusCache.klippyStatusTopic(a)
}

func (a *App) objectStatusTopic(name string) string {
	return a.statusCache.objectStatusTopic(a, name)
}
