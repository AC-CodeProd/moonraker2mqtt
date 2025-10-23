# Moonraker2MQTT

[![Go Version](https://img.shields.io/badge/Go-1.24-blue.svg)](https://golang.org/)
[![License: GPL-3.0](https://img.shields.io/badge/License-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)

Un pont performant et robuste entre Moonraker (Klipper) et MQTT, écrit en Go. Ce projet permet l'intégration transparente de votre imprimante 3D avec des systèmes domotiques comme Home Assistant, Node-RED, ou tout autre système compatible MQTT.

## 🚀 Fonctionnalités

- **Pont bidirectionnel** : Communication temps réel entre Moonraker et MQTT
- **Surveillance en temps réel** : État de l'imprimante, températures, progression d'impression
- **Contrôle à distance** : Envoi de commandes G-code et contrôle de l'imprimante via MQTT
- **Reconnexion automatique** : Gestion robuste des déconnexions réseau
- **Configuration flexible** : Support des variables d'environnement et fichiers YAML
- **Logs structurés** : Système de logging avancé avec différents niveaux
- **Support multi-plateforme** : Binaires disponibles pour Linux, Windows, et macOS (ARM64/AMD64)

## 📋 Table des matières

- [Installation](#-installation)
- [Configuration](#-configuration)
- [Utilisation](#-utilisation)
- [Commandes MQTT](#-commandes-mqtt)
- [Intégrations](#-intégrations)
- [Développement](#-développement)
- [Support](#-support)

## 🔧 Installation

### Binaire pré-compilé

1. Téléchargez le dernier binaire depuis les [releases GitHub](https://github.com/AC-CodeProd/moonraker2mqtt/releases)
2. Rendez-le exécutable :
```bash
chmod +x moonraker2mqtt-*-linux-amd64
sudo mv moonraker2mqtt-*-linux-amd64 /usr/local/bin/moonraker2mqtt
```

### Compilation depuis les sources

```bash
git clone https://github.com/AC-CodeProd/moonraker2mqtt.git
cd moonraker2mqtt
go mod download
go build -o moonraker2mqtt ./cmd/main.go
```

## ⚙️ Configuration

### Génération d'une configuration par défaut

```bash
moonraker2mqtt -generate-config
```

### Structure de configuration

```yaml
environment: development  # development | production | testing

moonraker:
  host: localhost                   # Adresse IP de Moonraker
  port: 7125                        # Port de Moonraker (défaut: 7125)
  api_key: ""                       # Clé API Moonraker (optionnel)
  ssl: false                        # Utiliser HTTPS/WSS
  timeout: 30                       # Timeout des requêtes (secondes)
  auto_reconnect: true              # Reconnexion automatique
  max_reconnect_attempts: 10        # Nombre max de tentatives
  call_interval: 2                  # Intervalle de surveillance (secondes)
  monitored_objects: |              # Objets Klipper à surveiller (JSON)
    {
      "print_stats": null,
      "toolhead": ["position"],
      "extruder": ["temperature", "target"],
      "heater_bed": ["temperature", "target"]
    }

mqtt:
  host: localhost                 # Broker MQTT
  port: 1883                      # Port MQTT (1883 non-TLS, 8883 TLS)
  username: ""                    # Nom d'utilisateur MQTT
  password: ""                    # Mot de passe MQTT  
  use_tls: false                  # Utiliser TLS/SSL
  client_id: moonraker2mqtt       # ID client MQTT
  topic_prefix: moonraker         # Préfixe des topics
  qos: 0                          # Qualité de service (0, 1, ou 2)
  retain: false                   # Messages persistants
  auto_reconnect: true            # Reconnexion automatique
  max_reconnect_attempts: 10      # Nombre max de tentatives
  commands_enabled: true          # Autoriser les commandes MQTT

logging:
  level: info                     # debug | info | warn | error
  format: text                    # text | json
```

### Variables d'environnement

Toutes les options de configuration peuvent être surchargées par des variables d'environnement :

```bash
export MOONRAKER_HOST=192.168.1.100
export MQTT_HOST=192.168.1.200
export MQTT_USERNAME=homeassistant
export MQTT_PASSWORD=secretpassword
export LOG_LEVEL=debug
```

## 🎯 Utilisation

### Démarrage basique

```bash
# Avec configuration par défaut
moonraker2mqtt

# Avec fichier de configuration personnalisé
moonraker2mqtt -config /path/to/config.yaml

# Afficher la version
moonraker2mqtt -version
```

### Structure des topics MQTT

Le bridge publie automatiquement sur ces topics :

```
moonraker/
├── state                    # État de connexion WebSocket
├── server/info             # Informations du serveur Moonraker
├── printer/info            # Informations de l'imprimante
├── klipper/state           # État de Klipper (ready, error, etc.)
├── objects/
│   ├── print_stats         # Statistiques d'impression
│   ├── toolhead           # Position de la tête d'impression
│   ├── extruder           # Températures extrudeur
│   └── heater_bed         # Températures lit chauffant
├── notifications/          # Notifications temps réel de Moonraker
│   ├── print_started
│   ├── print_paused
│   └── ...
└── commands               # Topic pour envoyer des commandes
```

### Exemples de données publiées

**État de l'imprimante** (`moonraker/klipper/state`) :
```
ready
```

**Statistiques d'impression** (`moonraker/objects/print_stats`) :
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

**Températures** (`moonraker/objects/extruder`) :
```json
{
  "temperature": 210.2,
  "target": 210.0,
  "power": 0.8
}
```

## 🎮 Commandes MQTT

Le bridge supporte l'envoi de commandes à l'imprimante via MQTT. Consultez le fichier [MQTT_COMMANDS.md](MQTT_COMMANDS.md) pour la documentation complète.

### Exemples rapides

```bash
# Pause d'impression
mosquitto_pub -h localhost -t "moonraker/commands" \
  -m '{"command": "pause"}'

# Chauffage extrudeur
mosquitto_pub -h localhost -t "moonraker/commands" \
  -m '{"command": "set_temperature", "params": {"heater": "extruder", "target": 210}}'

# G-code personnalisé
mosquitto_pub -h localhost -t "moonraker/commands" \
  -m '{"command": "gcode", "params": {"script": "G28"}}'
```

## 🏠 Intégrations

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

Exemple de flux Node-RED pour surveiller et contrôler l'imprimante :

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

## 🔄 Surveillance et maintenance

### Logs

Les logs sont écrits dans le dossier `logs/` :

```bash
# Suivre les logs en temps réel
tail -f logs/moonraker2mqtt.log

# Filtrer par niveau
grep "ERROR" logs/moonraker2mqtt.log
```

### Métriques de santé

Le bridge expose des métriques via les topics MQTT :

- `moonraker/state` : État de connexion WebSocket
- Logs structurés avec timestamps
- Reconnexions automatiques avec backoff exponentiel

### Service systemd

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

## 🛠 Développement

### Prérequis

- Go 1.24+

### Configuration de l'environnement de développement

```bash
git clone https://github.com/AC-CodeProd/moonraker2mqtt.git
cd moonraker2mqtt

# Installation des dépendances
go mod download

# Lancement en mode développement avec Air (rechargement automatique)
go install github.com/air-verse/air@latest
air

# Tests
go test -v ./...

# Tests avec couverture
go test -v -race -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### Structure du projet

```
moonraker2mqtt/
├── cmd/                    # Point d'entrée de l'application
│   ├── main.go
│   └── main_test.go
├── config/                 # Gestion de la configuration
│   ├── config.go
│   ├── struct.go
│   └── config_test.go
├── moonraker/             # Client Moonraker/Klipper
│   └── client.go
├── mqtt/                  # Client MQTT
│   └── paho_client.go
├── websocket/             # Client WebSocket
│   ├── client.go
│   ├── interface.go
│   ├── message.go
│   ├── struct.go
│   ├── retry.go
│   └── error.go
├── logger/                # Système de logging
│   └── logger.go
├── utils/                 # Utilitaires
│   └── utils.go
└── version/               # Informations de version
    └── version.go
```

### Contributions

1. Fork le projet
2. Créez une branche feature (`git checkout -b feature/amazing-feature`)
3. Committez vos changements (`git commit -m 'Add amazing feature'`)
4. Poussez vers la branche (`git push origin feature/amazing-feature`)
5. Ouvrez une Pull Request

### Build multi-plateforme

```bash
# Build manuel pour différentes architectures
GOOS=linux GOARCH=amd64 go build -o moonraker2mqtt-linux-amd64 ./cmd/main.go
GOOS=linux GOARCH=arm64 go build -o moonraker2mqtt-linux-arm64 ./cmd/main.go
GOOS=windows GOARCH=amd64 go build -o moonraker2mqtt-windows-amd64.exe ./cmd/main.go
GOOS=darwin GOARCH=amd64 go build -o moonraker2mqtt-darwin-amd64 ./cmd/main.go
GOOS=darwin GOARCH=arm64 go build -o moonraker2mqtt-darwin-arm64 ./cmd/main.go
```

## 🐛 Dépannage

### Problèmes courants

**Connexion WebSocket échoue** :
```bash
# Vérifiez la connectivité
curl http://moonraker-ip:7125/server/info

# Vérifiez les logs
grep "WebSocket" logs/moonraker2mqtt.log
```

**Connexion MQTT échoue** :
```bash
# Test de connectivité MQTT
mosquitto_pub -h mqtt-broker -t test -m "hello"

# Vérifiez les credentials
grep "MQTT" logs/moonraker2mqtt.log
```

**Performance** :
```bash
# Réduisez l'intervalle de surveillance
# Dans config.yaml : call_interval: 5  # au lieu de 2

# Limitez les objets surveillés
# Modifiez monitored_objects pour inclure uniquement les objets nécessaires
```

### Debug avancé

```bash
# Mode debug
export LOG_LEVEL=debug
moonraker2mqtt

# Trace réseau
tcpdump -i any -w capture.pcap host moonraker-ip

# Métriques système
htop
iotop
```

## 📄 License

Ce projet est sous licence GPL-3.0. Voir le fichier [LICENSE](LICENSE) pour plus de détails.

## 🤝 Support

- 🐛 [Issues GitHub](https://github.com/AC-CodeProd/moonraker2mqtt/issues)

## 🎯 Roadmap

---

**Développé avec ❤️ par [AC-CodeProd](https://github.com/AC-CodeProd)**