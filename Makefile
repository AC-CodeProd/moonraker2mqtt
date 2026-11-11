GO ?= go
TINYGO_TARGET ?= /src/targets/esp32s3-settings.json
LOCAL_SETTINGS_TARGET := $(filter /src/targets/esp32s3-settings.json targets/esp32s3-settings.json,$(TINYGO_TARGET))
LOCAL_TINYGO_TARGET := $(if $(LOCAL_SETTINGS_TARGET),build/local-target.json,$(TINYGO_TARGET))
# Fresh output directory: builds fail rather than replace an earlier image.
FIRMWARE_OUTPUT ?= $(CURDIR)/build/tinygo-0.44.0
FIRMWARE_LDFLAGS ?=
export FIRMWARE_LDFLAGS

.PHONY: build test vet firmware firmware-local test-firmware-adapter
build:
	mkdir -p build
	CGO_ENABLED=0 $(GO) build -o build/moonraker2mqtt ./cmd/moonraker2mqtt
test:
	$(GO) test ./...
vet:
	$(GO) vet ./...
test-firmware-adapter:
	$(GO) test -tags='natiu lowmem native_storage_fixture' ./mqtt ./websocket ./websocket/wire/lowmem ./bridge ./moonraker ./platform/esp32s3/network ./internal/tinygotcpfixture
firmware:
	python3 tools/firmware_build.py --target "$(TINYGO_TARGET)" --output "$(FIRMWARE_OUTPUT)"
# Advanced route: requires an already patched 0.44.0 toolchain; never mutates it.
firmware-local:
	python3 tools/firmware_build.py --check-local
	mkdir -p build
ifneq ($(LOCAL_SETTINGS_TARGET),)
	python3 tools/settings_target.py --root "$$(tinygo env TINYGOROOT)" --output build/local-target.json
endif
	GOEXPERIMENT=jsonv2 tinygo build -target="$(LOCAL_TINYGO_TARGET)" -scheduler=tasks -stack-size=8kb -opt=z -size=short -ldflags="$(FIRMWARE_LDFLAGS)" -o build/firmware-raw.bin ./cmd/moonraker2mqtt-esp32
	python3 tools/settings_image.py build/firmware-raw.bin --output build/firmware.bin
