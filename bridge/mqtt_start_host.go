//go:build !tinygo && !lowmem

package bridge

import "context"

func (a *App) connectMQTT(ctx context.Context) error { return a.mqttClient.Connect() }
