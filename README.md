# Moonraker2MQTT

[![Go Version](https://img.shields.io/badge/Go-1.24-blue.svg)](https://golang.org/)
[![License: GPL-3.0](https://img.shields.io/badge/License-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)

A performant and robust bridge between Moonraker (Klipper) and MQTT, written in Go. This project enables seamless integration of your 3D printer with home automation systems like Home Assistant, Node-RED, or any other MQTT-compatible system.

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


### ESP32-S3 firmware foundation (experimental)

The hosted application retains its YAML/.env loader, flags, file logging,
Paho adapter, polling, topics and command behavior. Firmware reuses the bridge
and Moonraker/WebSocket client, with native Wi-Fi (`espradio`), serial logging
and a bounded `natiu-mqtt` MQTT 3.1.1 adapter. It does **not** import Paho,
Gorilla WebSocket, YAML/dotenv or the host filesystem logger.

```sh
make test vet build
./build/moonraker2mqtt -version
make test-firmware-adapter
# Compile-only firmware, with empty credentials: boot rejects missing settings.
make firmware
```

Pinned compiler: `tinygo/tinygo:0.42.0`; target: `esp32s3-generic`.
The intended board is Waveshare ESP32-S3 Zero (FH4R2, 4 MB flash, 2 MB
PSRAM, GPIO21 RGB LED). This foundation does not assume PSRAM availability,
use the second core, drive the LED, or select `esp32s3-supermini`.
The compiler/build is validated, **not the board or live network path**.

Supply settings via TinyGo link-time strings (package
`moonraker2mqtt/platform/esp32s3`). A local compiler can use the same flags
with `make firmware-local`:

```sh
# Example values only: never commit real credentials or firmware containing them.
make firmware FIRMWARE_LDFLAGS="-X 'moonraker2mqtt/platform/esp32s3.WiFiSSID=my-network' -X 'moonraker2mqtt/platform/esp32s3.WiFiPassword=example-only' -X 'moonraker2mqtt/platform/esp32s3.MoonrakerHost=192.0.2.10' -X 'moonraker2mqtt/platform/esp32s3.MQTTHost=192.0.2.20'"
```

Required: `WiFiSSID`, `MoonrakerHost`, `MQTTHost`. `WiFiPassword` may be
empty for an open network. Optional: `MoonrakerPort` (7125), `MoonrakerAPIKey`,
`MQTTPort` (1883), `MQTTUsername`, `MQTTPassword`, `MQTTClientID`
(moonraker2mqtt-esp32), `MQTTTopicPrefix` (moonraker), `CommandsEnabled`
(false), `CallInterval` (5 seconds), `LogLevel` (info).
Commands are disabled by default; enabling them permits printer control.
Firmware and build logs/command history can expose embedded credentials.
Treat the resulting binary as a secret; use a private broker/trusted LAN.
Complex quoting in secrets requires correctly escaped linker flags; no
runtime environment/YAML/flash configuration is provided.

Limits: plain TCP MQTT and `ws://` only, MQTT QoS0, exact-topic subscriptions
(maximum 4), MQTT payloads up to 4096 bytes, topic strings up to 256 bytes,
4 queued incoming commands (overflow drops with a warning), 4 queued
WebSocket messages, 8 pending JSON-RPC requests, 16 KiB incoming WebSocket
frames. Oversized MQTT messages fail rather than pretending to publish;
QoS0 publishes do not retry potentially delivered messages. UNSUBSCRIBE is
sent to the broker and removes the local handler without waiting for UNSUBACK.
The serial console uses TinyGo's target standard output. No OTA, captive
portal, NVS persistence, TLS, watchdog or hardware-specific flashing automation
is included. Existing host reconnection logic is reused; independent Wi-Fi,
broker and printer reconnects, memory behavior and long soak tests remain
hardware acceptance gates. No live printer/broker is contacted by local tests.
`build/firmware.bin` is ignored by Git and is not added to hosted release assets.

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