//go:build tinygo && esp32s3

package main

import "moonraker2mqtt/platform/esp32s3"

func main() { esp32s3.Run() }
