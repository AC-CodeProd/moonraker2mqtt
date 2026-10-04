//go:build tinygo && esp32s3

package esp32s3

import (
	"context"
	"moonraker2mqtt/bridge"
	"moonraker2mqtt/logger"
	"moonraker2mqtt/mqtt"
	"os"
	"time"
	"tinygo.org/x/drivers/netdev"
	nl "tinygo.org/x/drivers/netlink"
	link "tinygo.org/x/espradio/netlink"
)

// Run boots the native ESP32-S3 radio before any shared network clients.
// A failed session is retried, but radio recovery and long-running stability
// still require hardware validation.
func Run() {
	time.Sleep(3 * time.Second)
	log := logger.NewSerial(os.Stdout, logger.ParseLogLevel(LogLevel))
	cfg, err := Config()
	if err != nil {
		log.Error("Firmware configuration: %v", err)
		for {
			time.Sleep(time.Minute)
		}
	}
	radio := &link.Esplink{}
	netdev.UseNetdev(radio)
	for {
		if err := radio.NetConnect(&nl.ConnectParams{Ssid: WiFiSSID, Passphrase: WiFiPassword}); err != nil {
			log.Warn("Wi-Fi connection failed: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}
		log.Info("Wi-Fi connected")
		break
	}
	for {
		client := mqtt.NewNatiuClient(cfg.MQTT, log)
		app := bridge.New(cfg, client, log)
		if err := app.Run(context.Background()); err != nil {
			log.Error("Bridge stopped: %v", err)
		}
		if err := client.Disconnect(); err != nil {
			log.Warn("MQTT cleanup: %v", err)
		}
		time.Sleep(5 * time.Second)
	}
}
