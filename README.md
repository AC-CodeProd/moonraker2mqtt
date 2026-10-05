# Moonraker2MQTT

[![Go Version](https://img.shields.io/badge/Go-1.24.4-blue.svg)](https://golang.org/)
[![License: GPL-3.0](https://img.shields.io/badge/License-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)

A performant and robust bridge between Moonraker (Klipper) and MQTT, written in Go. This project enables seamless integration of your 3D printer with home automation systems like Home Assistant, Node-RED, or any other MQTT-compatible system.

[Français](README_FR.md)

## Supported targets

- **Hosted application:** Linux, Windows and macOS; existing runtime behavior is preserved.
- **Experimental firmware:** ESP32-S3 with TinyGo, targeting the Waveshare ESP32-S3-Zero. Compilation is validated; on-device operation is not yet validated.

The ESP32-S3 work is currently on `feat/esp32s3-platform-structure`, not merged into `main`. Select this branch before following the source build instructions below. YAML, environment variables, file logs and systemd instructions apply to the hosted application only. See [ESP32-S3 firmware](#esp32-s3-firmware-experimental) for embedded settings and limitations.

## 🚀 Features

- **Bidirectional bridge**: Real-time communication between Moonraker and MQTT
- **Real-time monitoring**: Printer status, temperatures, print progress
- **Remote control**: Send G-code commands and control the printer via MQTT
- **Automatic reconnection**: Robust handling of network disconnections
- **Flexible configuration**: Support for environment variables and YAML files
- **Structured logging**: Advanced logging system with different levels
- **Multi-platform support**: Binaries available for Linux, Windows, and macOS (ARM64/AMD64)

## 📋 Table of Contents

- [Installation](#-installation)
- [Configuration](#-configuration)
- [Usage](#-usage)
- [MQTT Commands](#-mqtt-commands)
- [Integrations](#-integrations)
- [Development](#-development)
- [ESP32-S3 firmware](#esp32-s3-firmware-experimental)
- [Support](#-support)

## 🔧 Installation

### Pre-compiled binary

1. Download the latest binary from [GitHub releases](https://github.com/AC-CodeProd/moonraker2mqtt/releases)
2. Make it executable:
```bash
chmod +x moonraker2mqtt-*-linux-amd64
sudo mv moonraker2mqtt-*-linux-amd64 /usr/local/bin/moonraker2mqtt
```



### Build from source

```bash
git clone https://github.com/AC-CodeProd/moonraker2mqtt.git
cd moonraker2mqtt
git switch feat/esp32s3-platform-structure
go mod download
go build -o moonraker2mqtt ./cmd/moonraker2mqtt
```

## ⚙️ Configuration

### Generate default configuration

```bash
moonraker2mqtt -generate-config
```

### Configuration structure

```yaml
environment: development  # development | production | testing

moonraker:
  host: localhost                   # Moonraker IP address
  port: 7125                        # Moonraker port (default: 7125)
  api_key: ""                       # Moonraker API key (optional)
  ssl: false                        # Use HTTPS/WSS
  timeout: 30                       # Request timeout (seconds)
  auto_reconnect: true              # Automatic reconnection
  max_reconnect_attempts: 10        # Maximum number of attempts
  call_interval: 2                  # Monitoring interval (seconds)
  monitored_objects: |              # Klipper objects to monitor (JSON)
    {
      "print_stats": null,
      "toolhead": ["position"],
      "extruder": ["temperature", "target"],
      "heater_bed": ["temperature", "target"]
    }

mqtt:
  host: localhost                 # MQTT broker
  port: 1883                      # MQTT port (1883 non-TLS, 8883 TLS)
  username: ""                    # MQTT username
  password: ""                    # MQTT password  
  use_tls: false                  # Use TLS/SSL
  client_id: moonraker2mqtt       # MQTT client ID
  topic_prefix: moonraker         # Topic prefix
  qos: 0                          # Quality of service (0, 1, or 2)
  retain: false                   # Persistent messages
  auto_reconnect: true            # Automatic reconnection
  max_reconnect_attempts: 10      # Maximum number of attempts
  commands_enabled: true          # Allow MQTT commands

logging:
  level: info                     # debug | info | warn | error
  format: text                    # text | json
```

### Environment variables

All configuration options can be overridden by environment variables:

```bash
export MOONRAKER_HOST=192.168.1.100
export MQTT_HOST=192.168.1.200
export MQTT_USERNAME=homeassistant
export MQTT_PASSWORD=secretpassword
export LOG_LEVEL=debug
```

## 🎯 Usage

### Basic startup

```bash
# With default configuration
moonraker2mqtt

# With custom configuration file
moonraker2mqtt -config /path/to/config.yaml

# Show version
moonraker2mqtt -version
```

### MQTT topic structure

The bridge automatically publishes to these topics:

```
moonraker/
├── state                    # WebSocket connection state
├── server/info             # Moonraker server information
├── printer/info            # Printer information
├── klipper/state           # Klipper state (ready, error, etc.)
├── objects/
│   ├── print_stats         # Print statistics
│   ├── toolhead           # Print head position
│   ├── extruder           # Extruder temperatures
│   └── heater_bed         # Heated bed temperatures
├── notifications/          # Real-time Moonraker notifications
│   ├── print_started
│   ├── print_paused
│   └── ...
└── commands               # Topic for sending commands
```

### Examples of published data

**Printer state** (`moonraker/klipper/state`):
```
ready
```

**Print statistics** (`moonraker/objects/print_stats`):
```json
{
  "filename": "test_print.gcode",
  "total_duration": 1234.56,
  "print_duration": 1200.00,
  "filament_used": 125.45,
  "state": "printing",
  "message": "",
  "info": {
    "total_layer": 100,
    "current_layer": 45
  }
}
```

**Temperatures** (`moonraker/objects/extruder`):
```json
{
  "temperature": 210.2,
  "target": 210.0,
  "power": 0.8
}
```

## 🎮 MQTT Commands

The bridge supports sending commands to the printer via MQTT. See the [MQTT_COMMANDS.md](MQTT_COMMANDS.md) file for complete documentation.

### Quick examples

```bash
# Pause print
mosquitto_pub -h localhost -t "moonraker/commands" \
  -m '{"command": "pause"}'

# Heat extruder
mosquitto_pub -h localhost -t "moonraker/commands" \
  -m '{"command": "set_temperature", "params": {"heater": "extruder", "target": 210}}'

# Custom G-code
mosquitto_pub -h localhost -t "moonraker/commands" \
  -m '{"command": "gcode", "params": {"script": "G28"}}'
```

## 🏠 Integrations

### Home Assistant

```yaml
# configuration.yaml
mqtt:
  sensor:
    - name: "Printer State"
      state_topic: "moonraker/klipper/state"
      icon: mdi:printer-3d
    
    - name: "Print Progress"
      state_topic: "moonraker/objects/print_stats"
      value_template: "{{ (value_json.print_duration / value_json.total_duration * 100) | round(1) }}"
      unit_of_measurement: "%"
    
    - name: "Extruder Temperature"
      state_topic: "moonraker/objects/extruder"
      value_template: "{{ value_json.temperature }}"
      unit_of_measurement: "°C"

  button:
    - name: "Pause Print"
      command_topic: "moonraker/commands"
      payload_press: '{"command": "pause"}'
    
    - name: "Resume Print"
      command_topic: "moonraker/commands" 
      payload_press: '{"command": "resume"}'
```

### Node-RED

Example Node-RED flow to monitor and control the printer:

```json
[
  {
    "id": "mqtt-in",
    "type": "mqtt in",
    "topic": "moonraker/objects/+",
    "qos": "0",
    "broker": "mqtt-broker"
  },
  {
    "id": "parse-json",
    "type": "json",
    "property": "payload"
  },
  {
    "id": "temperature-alert",
    "type": "switch",
    "property": "payload.temperature",
    "rules": [
      {"t": "gt", "v": "250"}
    ]
  }
]
```

## 🔄 Monitoring and Maintenance

### Logs

Logs are written to the `logs/` folder:

```bash
# Follow logs in real-time
tail -f logs/moonraker2mqtt.log

# Filter by level
grep "ERROR" logs/moonraker2mqtt.log
```

### Health metrics

The bridge exposes metrics via MQTT topics:

- `moonraker/state`: WebSocket connection state
- Structured logs with timestamps
- Automatic reconnections with exponential backoff

### systemd service

```ini
# /etc/systemd/system/moonraker2mqtt.service
[Unit]
Description=Moonraker to MQTT
After=network.target

[Service]
Type=simple
User=pi
Group=pi
WorkingDirectory=/opt/moonraker2mqtt
ExecStart=/usr/local/bin/moonraker2mqtt -config /opt/moonraker2mqtt/config.yaml
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl enable moonraker2mqtt
sudo systemctl start moonraker2mqtt
sudo systemctl status moonraker2mqtt
```

## 🛠 Development

### Prerequisites

- Go 1.24.4+

### Development environment setup

```bash
git clone https://github.com/AC-CodeProd/moonraker2mqtt.git
cd moonraker2mqtt
git switch feat/esp32s3-platform-structure

# Install dependencies
go mod download

# Run in development mode with Air (automatic reload)
go install github.com/air-verse/air@latest
air

# Tests
go test -v ./...

# Tests with coverage
go test -v -race -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### Project structure

```text
cmd/moonraker2mqtt/        # Hosted CLI (Linux, Windows, macOS)
cmd/moonraker2mqtt-esp32/  # TinyGo ESP32-S3 entrypoint
bridge/                   # Shared application, topics, polling and commands
moonraker/                # Shared Moonraker JSON-RPC client
websocket/                # Shared transport; platform-specific resource limits
mqtt/interface.go         # Shared MQTT contract
mqtt/paho.go              # Hosted Paho adapter (!tinygo)
mqtt/natiu.go             # Firmware natiu adapter (tinygo or local natiu tests)
config/                   # Portable schemas/helpers; host-only YAML/env/fs loader
logger/                   # Portable contract/serial writer; host-only file logger
platform/host/            # Host assembly
platform/esp32s3/         # Link-time configuration and native Wi-Fi boot
utils/                    # Hosted application utilities
version/                  # Shared build metadata
```

### Contributing

1. Fork the project
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

### Multi-platform build

```bash
# Manual build for different architectures
GOOS=linux GOARCH=amd64 go build -o moonraker2mqtt-linux-amd64 ./cmd/moonraker2mqtt
GOOS=linux GOARCH=arm64 go build -o moonraker2mqtt-linux-arm64 ./cmd/moonraker2mqtt
GOOS=windows GOARCH=amd64 go build -o moonraker2mqtt-windows-amd64.exe ./cmd/moonraker2mqtt
GOOS=darwin GOARCH=amd64 go build -o moonraker2mqtt-darwin-amd64 ./cmd/moonraker2mqtt
GOOS=darwin GOARCH=arm64 go build -o moonraker2mqtt-darwin-arm64 ./cmd/moonraker2mqtt
```


## ESP32-S3 firmware (experimental)

### Requirements

- Waveshare ESP32-S3-Zero (ESP32-S3FH4R2), a USB-C data cable and a trusted 2.4 GHz Wi-Fi network.
- Moonraker and a private MQTT broker reachable from the board; use their network addresses, not `localhost`.
- Go 1.24.4+ and Make; Docker for `make firmware`, or local TinyGo 0.42.0 for `make firmware-local`.
- The firmware is built separately and is not included in the hosted GitHub release assets.

### Build and configuration

The Linux/Windows/macOS configuration, flags, Paho adapter, logs, topics and
commands are unchanged. The experimental S3 firmware now includes an offline
French setup page and a two-slot settings store; **host tests and compilation
pass, but the new portal/save/reboot path has not yet been tested on-device**.

```sh
make test vet build
make test-firmware-adapter
# Example only: use a unique WPA2 setup password, not this shared example.
make firmware FIRMWARE_LDFLAGS="-X moonraker2mqtt/platform/esp32s3.SetupPassword=replace-with-unique-password"
```

TinyGo is pinned to `tinygo/tinygo:0.42.0`. The custom
`targets/esp32s3-settings.json` inherits `esp32s3-supermini` for the USB console,
with an explicit internal-RAM-only linker layout for this S3 rev0.2/XMC 4MiB
board. `firmware-local` generates paths for a local TinyGo installation.
Both build rules honor `TINYGO_TARGET`: for example,
`make firmware-local TINYGO_TARGET=/absolute/path/custom-settings.json`.
Only the default settings target is translated to `build/local-target.json`;
an explicit custom target is passed through unchanged. Custom targets must keep
the S3 internal-RAM linker/IRAM vectors, RTC NOLOAD reservation and both settings
sectors intact; generic targets without those reservations are **not supported**.
Docker target paths must exist inside the container (normally under `/src`).
The image check/4MiB header remains enabled for both rules.
No PSRAM, second CPU, LED or generic S3 flash support is assumed.

Without saved settings or valid link-time fallback settings, join
`Moonraker-Setup` using the build's unique WPA2 password, then explicitly open
`http://192.168.4.1`. DHCP is enabled; automatic captive DNS is not implemented.
The form covers Wi-Fi, Moonraker host/port/API key, MQTT host/port/authentication,
client ID, topic prefix, polling and opt-in commands. It sends secrets only in a
bounded JSON POST; there is no LAN administration server or secret readback.

**Save means stage → reboot → commit before radio initialization**, not flash
writes while Wi-Fi runs. HTTP 202 means pending, not durable. The next boot
prints `SETTINGS: durable record loaded` only after CRC validation/readback.
Stored settings override the existing optional `-X` settings (`WiFiSSID`,
`WiFiPassword`, `MoonrakerHost`, `MQTTHost`, etc.). A missing setup password
fails closed instead of creating an open AP. `ForceSetup=true` can be supplied
at build time for explicit recovery without erasing existing records.
**This flag is permanent, not one-shot:** saving still returns to the portal on
every reboot. To resume the bridge, rebuild and reinstall with
`-X moonraker2mqtt/platform/esp32s3.ForceSetup=false` (or omit the flag); preserve
the settings sectors when replacing firmware.

Corrupt, unsupported-version or ambiguous storage blocks normal saves (HTTP 409)
and offers a separate repair. To discard those records, fill in replacement
settings, check the explicit two-sector erase confirmation and confirm the
browser dialog. The authenticated bounded `/repair` POST stages a CRC-covered
repair operation; only the next boot erases/reinitializes the two reserved
settings slots. **Unknown versions are erased only with that confirmation.**
Back up evidence first if needed. I/O/readback failures do not authorize repair;
diagnose the storage instead. HTTP 202 is still not a durability claim, and an
interrupted repair may lose both old records; no automatic repair/retry occurs.

### Validation status and limits

Unconfigured portal + full bridge: **1,297,447 bytes flash, 158,620 bytes static
RAM**. Configured build with commands enabled: **1,297,899 bytes flash,
158,740 bytes static RAM**. These exclude dynamic allocations and runtime
stacks. Both paths remain linked, so an empty configuration no longer produces
an artificially tiny build.

The two 4KiB settings sectors are `[0x1fe000, 0x200000)`, intentionally inside
the ROM's default 2MiB geometry on the actual 4MiB chip. The linker rejects image
overlap; no blind geometry patch is applied. RTC pending data is `NOLOAD` and is
excluded from image segments/BSS clearing. This is a statically checked layout,
**not yet proof of retained data across a physical reset**. The boot driver
checks chip/revision, legacy callback defaults, security state, core1 reset and
absence of PSRAM mappings; all flash access is sealed before starting radio.
Its cache-off wrapper and temporary exception vectors are IRAM/ROM-only.

See [setup safety, evidence and hardware gate](docs/ESP32S3_SETUP.md) before
flashing. New firmware flashing and settings erase/program tests require
explicit authorization; no full-chip erase or eFuse change is provided. The
existing independent ROM SHA warning remains disclosed, not fixed by this work.

Plain TCP MQTT and `ws://` only; QoS0; at most 4 exact-topic subscriptions,
4096-byte MQTT payloads, 256-byte topics, 4 queued incoming commands, 4 queued
WebSocket messages, 8 pending JSON-RPC requests and 16KiB incoming WebSocket
frames. No TLS, OTA, automatic captive discovery or watchdog qualification.
Wi-Fi initialization is attempted once per boot; association/DHCP failures are
retried with 5–30 second backoff and recovery setup after six failures, without
erasing settings. The local adapter reserves two simultaneous TCP slots for MQTT
and Moonraker and joins the packet pump before replacing a failed pre-bridge
stack. Association loss cancels the bridge; recovery uses a software reset rather
than reusing a descriptor table while WebSocket workers may still exist. This
reset cancels pending RTC data and preserves durable settings. These paths and
two real in-memory lneto sockets are regression-tested and the adapter compiles
with the pinned driver; on-device recovery and long-running memory behavior
remain unqualified. No production printer or broker was contacted by tests.
Firmware and stored records contain plaintext secrets; keep build artifacts
private and use a trusted LAN/broker. Linux limits remain unchanged.

## 🐛 Troubleshooting

### Common issues

**WebSocket connection fails**:
```bash
# Check connectivity
curl http://moonraker-ip:7125/server/info

# Check logs
grep "WebSocket" logs/moonraker2mqtt.log
```

**MQTT connection fails**:
```bash
# Test MQTT connectivity
mosquitto_pub -h mqtt-broker -t test -m "hello"

# Check credentials
grep "MQTT" logs/moonraker2mqtt.log
```

**Performance**:
```bash
# Reduce monitoring interval
# In config.yaml: call_interval: 5  # instead of 2

# Limit monitored objects
# Modify monitored_objects to include only necessary objects
```

### Advanced debugging

```bash
# Debug mode
export LOG_LEVEL=debug
moonraker2mqtt

# Network trace
tcpdump -i any -w capture.pcap host moonraker-ip

# System metrics
htop
iotop
```

## 📄 License

This project is licensed under GPL-3.0. See the [LICENSE](LICENSE) file for more details.

## 🤝 Support

- 🐛 [GitHub Issues](https://github.com/AC-CodeProd/moonraker2mqtt/issues)

## 🎯 Roadmap

---

**Developed with ❤️ by [AC-CodeProd](https://github.com/AC-CodeProd)**