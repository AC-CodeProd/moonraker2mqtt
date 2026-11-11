GO ?= go
TINYGO_IMAGE := tinygo/tinygo@sha256:7362ad526b5ed1b675cd7a101f295eb3e3d6d7aa32106a6a88fc49d7dc7f252f
TINYGO_TARGET ?= targets/esp32s3-settings.json
FIRMWARE_OUTPUT ?= $(CURDIR)/build/stock-tinygo-0.44.0
MODULE_CACHE ?= $(shell $(GO) env GOMODCACHE)
FIRMWARE_LDFLAGS ?=
export FIRMWARE_LDFLAGS

.PHONY: build test vet firmware test-firmware-adapter
build:
	mkdir -p build
	CGO_ENABLED=0 $(GO) build -o build/moonraker2mqtt ./cmd/moonraker2mqtt
test:
	$(GO) test ./...
vet:
	$(GO) vet ./...
test-firmware-adapter:
	$(GO) test -tags='natiu lowmem native_storage_fixture' ./mqtt ./websocket ./websocket/wire/lowmem ./bridge ./moonraker ./platform/esp32s3/network
# Stock compiler only. Outputs are NOT an installation/ROM-boot qualification.
# In particular firmware-raw.bin still needs independently reviewed image-header
# formatting (RAM digest + appended XIP); see both READMEs before any installation.
firmware:
	@test -d "$(MODULE_CACHE)" || { printf '%s\n' 'Populate the pinned Go module cache first'; exit 1; }
	@set -eu; umask 077; mkdir -p "$$(dirname "$(FIRMWARE_OUTPUT)")"; mkdir "$(FIRMWARE_OUTPUT)"; mkdir "$(FIRMWARE_OUTPUT)/home" "$(FIRMWARE_OUTPUT)/cache" "$(FIRMWARE_OUTPUT)/tmp" "$(FIRMWARE_OUTPUT)/gopath"
	python3 -c 'import json,pathlib; p=pathlib.Path("$(TINYGO_TARGET)"); t=json.loads(p.read_text()); t["ldflags"]=[f for f in t.get("ldflags",[]) if not f.startswith("-Map=")]+["-Map=/out/firmware.map"]; o=pathlib.Path("$(FIRMWARE_OUTPUT)/target.json"); o.write_text(json.dumps(t)+"\n"); o.chmod(0o600)'
	docker run --pull=never --network=none --read-only --user "$$(id -u):$$(id -g)" --cap-drop=ALL --security-opt=no-new-privileges --entrypoint /bin/sh \
		-v "$(CURDIR):/src:ro" -v "$(MODULE_CACHE):/modules:ro" -v "$(FIRMWARE_OUTPUT):/out:rw" \
		-e HOME=/out/home -e XDG_CACHE_HOME=/out/cache -e GOCACHE=/out/cache/go -e TMPDIR=/out/tmp -e GOPATH=/out/gopath -e GOMODCACHE=/modules \
		-e GOPROXY=off -e GOSUMDB=off -e GOTOOLCHAIN=local -e GOTELEMETRY=off -e GOEXPERIMENT=jsonv2 -e FIRMWARE_LDFLAGS \
		-w /src $(TINYGO_IMAGE) -ec 'umask 077; tinygo version; go version; tinygo build -target=/out/target.json -scheduler=tasks -stack-size=8kb -opt=z -size=short -ldflags="$$FIRMWARE_LDFLAGS" -o /out/firmware.elf ./cmd/moonraker2mqtt-esp32; cp /out/firmware.map /out/firmware-elf.map; tinygo build -target=/out/target.json -scheduler=tasks -stack-size=8kb -opt=z -size=short -ldflags="$$FIRMWARE_LDFLAGS" -o /out/firmware-raw.bin ./cmd/moonraker2mqtt-esp32'
