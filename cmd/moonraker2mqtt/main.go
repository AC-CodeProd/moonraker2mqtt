//go:build !tinygo

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"moonraker2mqtt/config"
	"moonraker2mqtt/platform/host"
	"moonraker2mqtt/version"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const DEFAULT_CONFIG_FILE = "config.yaml"

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

	app, err := host.NewApp(*configFile)
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
