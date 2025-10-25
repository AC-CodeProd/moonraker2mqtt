package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
	"moonraker2mqtt/moonraker"
	"moonraker2mqtt/mqtt"
	"moonraker2mqtt/version"
)

const (
	DEFAULT_CONFIG_FILE = "config.yaml"
)

type App struct {
	config          *config.Config
	moonrakerClient *moonraker.Client
	mqttClient      mqtt.MQTTClient
	logger          logger.Logger
}

func NewApp(configFile string) (*App, error) {
	cfg, err := config.LoadOrCreateConfig(configFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	logger := logger.New(&cfg.Logging, cfg.Environment)

	if logger == nil {
		return nil, fmt.Errorf("failed to create logger")
	}

	mqttClient := mqtt.NewPahoClient(
		cfg.MQTT.Host,
		cfg.MQTT.Port,
		cfg.MQTT.ClientID,
		cfg.MQTT.Username,
		cfg.MQTT.Password,
		cfg.MQTT.UseTLS,
		logger,
	)

	app := &App{
		config:     cfg,
		mqttClient: mqttClient,
		logger:     logger,
	}

	app.moonrakerClient = moonraker.NewClient(&cfg.Moonraker, logger, app)

	return app, nil
}

func (a *App) OnStateChanged(state string) {
	a.logger.Debug("Moonraker state changed: %s", state)

	if a.mqttClient.IsConnected() {
		topic := fmt.Sprintf("%s/state", a.config.MQTT.TopicPrefix)
		payload := []byte(state)
		if err := a.mqttClient.Publish(topic, payload, a.config.MQTT.QoS, a.config.MQTT.Retain, 3); err != nil {
			a.logger.Error("Failed to publish state to MQTT after retries: %v", err)
		}
	}
}

func (a *App) OnNotification(method string, params any) {
	a.logger.Debug("Received notification: %s", method)

	if a.mqttClient.IsConnected() {
		topic := fmt.Sprintf("%s/notifications/%s", a.config.MQTT.TopicPrefix, method)

		data, err := json.Marshal(params)
		if err != nil {
			a.logger.Error("Failed to marshal notification params: %v", err)
			return
		}

		if err := a.mqttClient.Publish(topic, data, a.config.MQTT.QoS, a.config.MQTT.Retain, 3); err != nil {
			a.logger.Error("Failed to publish notification to MQTT after retries: %v", err)
		}
	}
}

func (a *App) OnException(err error) {
	a.logger.Error("Moonraker exception: %v", err)
}

func (a *App) Run(ctx context.Context) error {
	a.logger.Info("Starting Moonraker2MQTT")

	if err := a.mqttClient.Connect(); err != nil {
		return fmt.Errorf("failed to connect to MQTT broker: %w", err)
	}
	defer func() {
		if err := a.mqttClient.Disconnect(); err != nil {
			a.logger.Error("Failed to disconnect from MQTT broker: %v", err)
		}
	}()

	if err := a.moonrakerClient.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to Moonraker: %w", err)
	}
	defer func() {
		if err := a.moonrakerClient.Disconnect(); err != nil {
			a.logger.Error("Failed to disconnect from Moonraker: %v", err)
		}
	}()

	a.logger.Info("Successfully connected to both Moonraker and MQTT")

	if a.config.MQTT.CommandsEnabled {
		commandTopic := fmt.Sprintf("%s/%s", a.config.MQTT.TopicPrefix, "commands")
		if err := a.mqttClient.Subscribe(commandTopic, a.moonrakerClient.HandleCommand); err != nil {
			a.logger.Warn("Failed to subscribe to command topic %s: %v", commandTopic, err)
		} else {
			a.logger.Info("Subscribed to command topic: %s", commandTopic)
		}
	}

	maxRetries := 3
	for retries := 0; retries < maxRetries; retries++ {
		if err := a.publishInitialInfo(ctx); err != nil {
			a.logger.Warn("Failed to publish initial info (attempt %d/%d): %v", retries+1, maxRetries, err)
			if retries == maxRetries-1 {
				a.logger.Error("Failed to publish initial info after %d attempts, continuing anyway", maxRetries)
			} else {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Second * time.Duration(retries+1)):
				}
			}
		} else {
			a.logger.Info("Successfully published initial info")
			break
		}
	}

	go a.periodicMonitoring(ctx)

	<-ctx.Done()
	a.logger.Info("Shutting down...")

	return nil
}

func (a *App) publishInitialInfo(ctx context.Context) error {
	serverInfo, err := a.moonrakerClient.GetServerInfo(ctx)
	if err != nil {
		return fmt.Errorf("failed to get server info: %w", err)
	}

	data, err := json.Marshal(serverInfo)
	if err != nil {
		return fmt.Errorf("failed to marshal server info: %w", err)
	}

	topic := fmt.Sprintf("%s/server/info", a.config.MQTT.TopicPrefix)
	if err := a.mqttClient.Publish(topic, data, a.config.MQTT.QoS, a.config.MQTT.Retain, 3); err != nil {
		return fmt.Errorf("failed to publish server info: %w", err)
	}

	printerInfo, err := a.moonrakerClient.GetHostInfo(ctx)
	if err != nil {
		return fmt.Errorf("failed to get printer info: %w", err)
	}

	data, err = json.Marshal(printerInfo)
	if err != nil {
		return fmt.Errorf("failed to marshal printer info: %w", err)
	}

	topic = fmt.Sprintf("%s/printer/info", a.config.MQTT.TopicPrefix)
	if err := a.mqttClient.Publish(topic, data, a.config.MQTT.QoS, a.config.MQTT.Retain, 3); err != nil {
		return fmt.Errorf("failed to publish printer info: %w", err)
	}

	return nil
}

func (a *App) periodicMonitoring(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(a.config.Moonraker.CallInterval) * time.Second)
	defer ticker.Stop()

	consecutiveErrors := 0
	maxConsecutiveErrors := 5

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := a.publishStatus(ctx); err != nil {
				consecutiveErrors++
				a.logger.Error("Failed to publish periodic status (error %d/%d): %v", consecutiveErrors, maxConsecutiveErrors, err)

				if consecutiveErrors >= maxConsecutiveErrors {
					a.logger.Warn("Too many consecutive errors, slowing down polling interval")
					ticker.Stop()
					ticker = time.NewTicker(time.Duration(a.config.Moonraker.CallInterval*2) * time.Second)
				}
			} else {
				if consecutiveErrors > 0 {
					a.logger.Info("Successfully published status after %d errors, resuming normal polling", consecutiveErrors)
					consecutiveErrors = 0
					ticker.Stop()
					ticker = time.NewTicker(time.Duration(a.config.Moonraker.CallInterval) * time.Second)
				}
			}
		}
	}
}

func (a *App) publishStatus(ctx context.Context) error {
	klippyState, err := a.moonrakerClient.GetKlippyState(ctx)
	if err != nil {
		return fmt.Errorf("failed to get klipper state: %w", err)
	}

	topic := fmt.Sprintf("%s/klipper/state", a.config.MQTT.TopicPrefix)
	if err := a.mqttClient.Publish(topic, []byte(klippyState), a.config.MQTT.QoS, false, 3); err != nil {
		return fmt.Errorf("failed to publish klipper state: %w", err)
	}

	objects, err := a.config.Moonraker.GetMonitoredObjects()
	if err != nil {
		a.logger.Warn("Failed to get monitored objects from config, using defaults: %v", err)
		objects = map[string]any{
			"print_stats": nil,
			"toolhead":    []string{"position"},
			"extruder":    []string{"temperature", "target"},
			"heater_bed":  []string{"temperature", "target"},
		}
	}

	result, err := a.moonrakerClient.QueryObjects(ctx, objects)
	if err != nil {
		return fmt.Errorf("failed to query objects: %w", err)
	}

	errorCount := 0
	totalObjects := len(result)
	for objectName, objectData := range result {
		if objectName == "eventtime" {
			continue
		}

		data, err := json.Marshal(objectData)
		if err != nil {
			a.logger.Error("Failed to marshal object %s: %v", objectName, err)
			errorCount++
			continue
		}

		topic := fmt.Sprintf("%s/objects/%s", a.config.MQTT.TopicPrefix, objectName)
		if err := a.mqttClient.Publish(topic, data, a.config.MQTT.QoS, false, 3); err != nil {
			a.logger.Error("Failed to publish object %s after retries: %v", objectName, err)
			errorCount++
		}
	}

	if errorCount > 0 {
		a.logger.Warn("Published objects with %d/%d errors", errorCount, totalObjects)
		if errorCount >= totalObjects/2 {
			return fmt.Errorf("too many object publication failures (%d/%d)", errorCount, totalObjects)
		}
	}

	return nil
}

func main() {
	configFile := flag.String("config", DEFAULT_CONFIG_FILE, "Configuration file path")
	generateConfig := flag.Bool("generate-config", false, "Generate a default configuration file and exit")
	showVersion := flag.Bool("version", false, "Show version information and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("moonraker2mqtt version %s\n", version.Version)
		fmt.Printf("Git Commit: %s\n", version.GitCommit)
		fmt.Printf("Git URL: %s\n", version.GitURL)
		fmt.Printf("Build Date: %s\n", version.BuildDate)
		return
	}

	if *generateConfig {
		err := config.GenerateDefaultConfig(*configFile)
		if err != nil {
			log.Fatalf("Failed to generate config: %v", err)
		}
		fmt.Printf("Default configuration generated at %s\n", *configFile)
		return
	}

	app, err := NewApp(*configFile)
	if err != nil {
		log.Fatalf("Failed to create app: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sigCount := 0
		for {
			<-sigChan
			sigCount++
			if sigCount == 1 {
				log.Println("Received shutdown signal")
				log.Println("Initiating graceful shutdown... (press Ctrl+C again to force quit)")
				cancel()

				go func() {
					time.Sleep(10 * time.Second)
					log.Println("Force shutdown after 10 seconds")
					os.Exit(1)
				}()
			} else {
				log.Println("Force quit requested")
				os.Exit(1)
			}
		}
	}()

	if err := app.Run(ctx); err != nil {
		log.Fatalf("Application error: %v", err)
	}

	log.Println("Application shutdown complete")
}
