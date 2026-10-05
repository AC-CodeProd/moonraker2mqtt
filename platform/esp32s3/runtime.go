//go:build tinygo && esp32s3

package esp32s3

import (
	"context"
	"moonraker2mqtt/bridge"
	"moonraker2mqtt/logger"
	"moonraker2mqtt/mqtt"
	"moonraker2mqtt/platform/esp32s3/network"
	"moonraker2mqtt/platform/esp32s3/store"
	"os"
	"time"
	"tinygo.org/x/drivers/netdev"
	nl "tinygo.org/x/drivers/netlink"
)

// Run owns radio state. No flash access is possible once any radio starts.
func Run() {
	println("BOOT: settings begin (boot-only commit/read)")
	settings, savedErr := bootSettings()
	println("BOOT: settings complete")
	sealBootFlash()
	println("BOOT: flash sealed")
	memorySnapshot("settings")
	time.Sleep(3 * time.Second)
	log := logger.NewSerial(os.Stdout, logger.ParseLogLevel(LogLevel))
	cfg, err := Config()
	ssid, password := WiFiSSID, WiFiPassword
	if savedErr == nil {
		cfg = settings.Config()
		err = cfg.Validate()
		ssid, password = settings.SSID, settings.WiFiPassword
		println("SETTINGS: durable record loaded")
	}
	if err == nil {
		err = applyQualificationSafety(cfg) // Apply AFTER durable settings override.
	}
	if QualificationReadOnly == "true" {
		println("QUALIFICATION: commands inhibited; settings writes disabled")
	}
	radio := newRadioLink()
	netdev.UseNetdev(radio)
	setupMode := func() {
		println("SETUP: entering recovery; durable settings preserved")
		if e := runPortal(radio, savedErr); e != nil {
			log.Error("Setup portal unavailable; reset or rebuild required")
		}
		safeIdle()
	}
	if ForceSetup == "true" || (savedErr != nil && savedErr != store.ErrMissing) || err != nil {
		setupMode()
		return
	}
	failures := 0
	for {
		println("BOOT: station connection begin")
		memorySnapshot("pre-radio")
		if err := radio.NetConnect(&nl.ConnectParams{Ssid: ssid, Passphrase: password}); err != nil {
			// Lifecycle returns only fixed stage names, never the driver's error text.
			log.Warn("Wi-Fi unavailable: %v", err)
			if err == network.ErrEnable || err == network.ErrShutdown {
				log.Error("Radio recovery unsafe; reset required, settings preserved")
				safeIdle()
			}
			failures++
			if failures >= 6 {
				setupMode()
				return
			}
			time.Sleep(network.Backoff(failures))
			continue
		}
		failures = 0
		log.Info("Wi-Fi association and DHCP complete")
		memorySnapshot("wifi")
		client := mqtt.NewNatiuClient(cfg.MQTT, log)
		app := bridge.New(cfg, client, log)
		memorySnapshot("client-init")
		err := network.Session(context.Background(), func() bool {
			memoryHeartbeat()
			return radio.connected()
		}, func(ctx context.Context) error {
			defer client.Disconnect() // Covers partial MQTT Connect failure as well.
			return app.Run(ctx)
		}, 250*time.Millisecond, 10*time.Second)
		if err == network.ErrShutdown {
			log.Error("Bridge shutdown timed out; reset required before network reuse")
			cancelPending() // An unrelated recovery reset must not commit RTC data.
			rebootSave()
		}
		// The shared WebSocket client starts an uncancellable DialConfig worker
		// and does not join every reconnect/monitor worker at Disconnect. Do not
		// replace its descriptor table/stack in this boot, even when Run returns.
		// Recovery is a bounded software reset, preserving durable settings and
		// invalidating RTC pending data, rather than overlapping old/new clients.
		log.Warn("Bridge session ended; recovery reset after bounded backoff")
		time.Sleep(network.Backoff(1))
		cancelPending()
		rebootSave()
	}
}

func safeIdle() {
	for {
		time.Sleep(time.Minute)
	}
}
