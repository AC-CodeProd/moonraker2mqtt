GO ?= go
TINYGO_IMAGE ?= tinygo/tinygo:0.42.0
TINYGO_TARGET ?= esp32s3-generic
# Example: FIRMWARE_LDFLAGS="-X moonraker2mqtt/platform/esp32s3.WiFiSSID=..."
FIRMWARE_LDFLAGS ?=

.PHONY: build test vet firmware firmware-local test-firmware-adapter
build:
	mkdir -p build
	CGO_ENABLED=0 $(GO) build -o build/moonraker2mqtt ./cmd/moonraker2mqtt
test:
	$(GO) test ./...
vet:
	$(GO) vet ./...
test-firmware-adapter:
	$(GO) test -tags=natiu ./mqtt
firmware:
	mkdir -p build
	docker run --rm -v "$(CURDIR):/src" -w /src $(TINYGO_IMAGE) tinygo build -target=$(TINYGO_TARGET) -size=short -ldflags="$(FIRMWARE_LDFLAGS)" -o /src/build/firmware.bin ./cmd/moonraker2mqtt-esp32
firmware-local:
	mkdir -p build
	tinygo build -target=$(TINYGO_TARGET) -size=short -ldflags="$(FIRMWARE_LDFLAGS)" -o build/firmware.bin ./cmd/moonraker2mqtt-esp32
