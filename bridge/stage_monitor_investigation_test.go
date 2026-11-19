package bridge

import (
	"context"
	"io"
	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
	"testing"
	"time"
)

func TestInvestigationMonitorDiagnosticStages(t *testing.T) {
	if !testMCUSessionPolicy {
		t.Skip("MCU recovery policy only")
	}
	for _, drop := range []string{"mqtt", "moonraker"} {
		t.Run(drop, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
			defer cancel()
			p := &sessionProbe{drop: drop, cancel: cancel}
			cfg := config.DefaultConfig()
			cfg.MQTT.CommandsEnabled = false
			cfg.Moonraker.CallInterval = 1
			app := New(cfg, sessionMQTT{p}, logger.NewSerial(io.Discard, logger.ERROR))
			app.moonrakerClient = sessionMoonraker{p}
			err := app.Run(ctx)
			wantMQTT, wantMR := 1, 1
			if drop == "mqtt" {
				wantMQTT = 2
			} else {
				wantMR = 2
			}
			if err != nil || ctx.Err() == nil || app.DiagnosticStage() != 4 || p.mqttConnects != wantMQTT || p.moonConnects != wantMR {
				t.Fatalf("recovery must stay in monitoring until context stop: stage=%d err=%v ctx=%v connects=%d/%d", app.DiagnosticStage(), err, ctx.Err(), p.mqttConnects, p.moonConnects)
			}

		})
	}
}
