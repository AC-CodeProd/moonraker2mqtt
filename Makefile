GO ?= go
TINYGO_IMAGE ?= tinygo/tinygo:0.42.0
TINYGO_TARGET ?= /src/targets/esp32s3-settings.json
# Translate only the default settings target for local installations. Explicit
# overrides are passed through, and must retain this board's reserved layout.
LOCAL_SETTINGS_TARGET := $(filter /src/targets/esp32s3-settings.json targets/esp32s3-settings.json,$(TINYGO_TARGET))
LOCAL_TINYGO_TARGET := $(if $(LOCAL_SETTINGS_TARGET),build/local-target.json,$(TINYGO_TARGET))
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
	docker run -v "$(CURDIR):/src" -w /src $(TINYGO_IMAGE) tinygo build -target=$(TINYGO_TARGET) -size=short -ldflags="$(FIRMWARE_LDFLAGS)" -o /src/build/firmware-raw.bin ./cmd/moonraker2mqtt-esp32
	python3 tools/settings_image.py build/firmware-raw.bin --output build/firmware.bin
firmware-local:
	mkdir -p build
ifneq ($(LOCAL_SETTINGS_TARGET),)
	python3 tools/settings_target.py --root "$$(tinygo env TINYGOROOT)" --output build/local-target.json
endif
	tinygo build -target="$(LOCAL_TINYGO_TARGET)" -size=short -ldflags="$(FIRMWARE_LDFLAGS)" -o build/firmware-raw.bin ./cmd/moonraker2mqtt-esp32
	python3 tools/settings_image.py build/firmware-raw.bin --output build/firmware.bin
