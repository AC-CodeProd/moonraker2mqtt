package bridge

// The returned selection is owned by the bridge. Adapters must only read it,
// including nested slices; it may be reused on MCU builds.
func (a *App) freshStatusObjects() (map[string]any, error) {
	objects, err := a.config.Moonraker.GetMonitoredObjects()
	if err != nil {
		objects = map[string]any{"print_stats": nil, "toolhead": []string{"position"}, "extruder": []string{"temperature", "target"}, "heater_bed": []string{"temperature", "target"}}
	}
	return objects, err
}
func (a *App) warnStatusSelection(err error) {
	if err != nil {
		a.logger.Warn("Failed to get monitored objects from config, using defaults: %v", err)
	}
}
