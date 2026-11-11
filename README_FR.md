# Moonraker2MQTT

[![Go Version](https://img.shields.io/badge/Go-1.24.4-blue.svg)](https://golang.org/)
[![License: GPL-3.0](https://img.shields.io/badge/License-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)

Un pont performant et robuste entre Moonraker (Klipper) et MQTT, écrit en Go. Ce projet permet l'intégration transparente de votre imprimante 3D avec des systèmes domotiques comme Home Assistant, Node-RED, ou tout autre système compatible MQTT.

[English](README.md)

## Cibles disponibles

- **Application hôte :** Linux, Windows et macOS ; comportement existant conservé.
- **Firmware expérimental :** ESP32-S3 avec TinyGo, pour le Waveshare ESP32-S3-Zero. La compilation est vérifiée ; le fonctionnement sur carte reste à valider.

Le portage ESP32-S3 est actuellement sur `feat/esp32s3-platform-structure`, sans fusion dans `main`. Sélectionner cette branche avant de suivre les commandes de compilation ci-dessous. Les instructions YAML, variables d’environnement, logs fichiers et systemd concernent uniquement l’application hôte. Voir [Firmware ESP32-S3](#firmware-esp32-s3-expérimental) pour la configuration embarquée et ses limites.

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
- [Firmware ESP32-S3](#firmware-esp32-s3-expérimental)
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
git switch feat/esp32s3-platform-structure
go mod download
go build -o moonraker2mqtt ./cmd/moonraker2mqtt
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

- Go 1.24.4+

### Configuration de l'environnement de développement

```bash
git clone https://github.com/AC-CodeProd/moonraker2mqtt.git
cd moonraker2mqtt
git switch feat/esp32s3-platform-structure

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

```text
cmd/moonraker2mqtt/        # Application hôte (Linux, Windows, macOS)
cmd/moonraker2mqtt-esp32/  # Point d’entrée TinyGo ESP32-S3
bridge/                   # Logique commune : topics, surveillance et commandes
moonraker/                # Client JSON-RPC Moonraker partagé
websocket/                # Transport partagé et limites propres à chaque cible
mqtt/interface.go         # Contrat MQTT commun
mqtt/paho.go              # Adaptateur Paho pour l’hôte (!tinygo)
mqtt/natiu.go             # Adaptateur natiu pour le firmware et les tests locaux
config/                   # Configuration portable ; chargement YAML/env côté hôte
logger/                   # Interface et logs série ; logs fichiers côté hôte
platform/host/            # Assemblage de l’application hôte
platform/esp32s3/         # Configuration à la compilation et démarrage Wi-Fi
utils/                    # Utilitaires de l’application hôte
version/                  # Métadonnées de compilation communes
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
GOOS=linux GOARCH=amd64 go build -o moonraker2mqtt-linux-amd64 ./cmd/moonraker2mqtt
GOOS=linux GOARCH=arm64 go build -o moonraker2mqtt-linux-arm64 ./cmd/moonraker2mqtt
GOOS=windows GOARCH=amd64 go build -o moonraker2mqtt-windows-amd64.exe ./cmd/moonraker2mqtt
GOOS=darwin GOARCH=amd64 go build -o moonraker2mqtt-darwin-amd64 ./cmd/moonraker2mqtt
GOOS=darwin GOARCH=arm64 go build -o moonraker2mqtt-darwin-arm64 ./cmd/moonraker2mqtt
```


## Firmware ESP32-S3 (expérimental)

### Prérequis

- Waveshare ESP32-S3-Zero (ESP32-S3FH4R2), câble USB-C de données et réseau Wi-Fi 2,4 GHz de confiance.
- Moonraker et un broker MQTT privé accessibles depuis la carte ; utiliser leurs adresses réseau, pas `localhost`.
- Go 1.24.4+ et Make ; Docker avec l’image Linux/arm64 TinyGo 0.44.0 figée et un cache de modules déjà disponible (compilation hors ligne).
- Le firmware se compile séparément et n’est pas inclus dans les releases GitHub de l’application hôte.

### Compilation et configuration

L’application Linux/Windows/macOS garde sa configuration, ses flags, Paho,
les logs, topics et commandes existants. Le firmware S3 ajoute une page de
configuration française hors ligne et un stockage à deux slots. **Tests hôte
et compilation validés ; nouveau parcours portail/sauvegarde/redémarrage non
encore validé sur la carte.**

```sh
make test vet build
make test-firmware-adapter
# Exemple uniquement : choisir un mot de passe WPA2 unique.
make firmware FIRMWARE_LDFLAGS="-X moonraker2mqtt/platform/esp32s3.SetupPassword=remplacer-par-un-secret-unique"
```

La compilation utilise **TinyGo 0.44.0 STANDARD / Go 1.27.3 / LLVM 22.1.4**,
image ARM64 exacte
`tinygo/tinygo@sha256:7362ad526b5ed1b675cd7a101f295eb3e3d6d7aa32106a6a88fc49d7dc7f252f`.
Aucune bibliothèque Go/TinyGo, aucun runtime ni compilateur n'est remplacé.
Précharger cette image et le cache des modules épinglés (`go mod download`) :
la compilation firmware ne télécharge ni image ni dépendance. Seules les
adaptations réseau distribuées dans `third_party/lneto` et
`third_party/natiu-mqtt` sont sélectionnées par `go.mod`.

`make firmware` monte sources et modules en lecture seule, coupe le réseau,
retire les capacités et utilise l'UID courant avec des caches neufs dans un
répertoire privé `FIRMWARE_OUTPUT` (`build/stock-tinygo-0.44.0` par défaut).
Choisir un nouveau répertoire à chaque compilation. Les sorties sont
**`firmware.elf` et `firmware-raw.bin`, pas une image prête à installer**.
Les conteneurs arrêtés sont conservés pour inspection. La cible par défaut
`targets/esp32s3-settings.json` hérite de `esp32s3-supermini` et conserve le
linker applicatif, les secteurs réservés, la RTC NOLOAD et les vecteurs IRAM.
Ni PSRAM utilisable, second cœur ni LED ne sont supposés.

**Le formatage de l'image est un prérequis externe.** Le format RAM suivi de XIP
nécessite un formateur revu qui fixe l'en-tête 4Mio puis recalcule le digest RAM
à l'offset déduit de la table des segments, sans changer les octets XIP ajoutés.
Ce formateur et les outils de qualification matérielle ne sont pas distribués
dans ce dépôt. Ne pas appliquer une correction SHA aux 32 derniers octets,
flasher la sortie brute ni présenter la construction d'installation comme
adéquate avec le seul dépôt. Une validation de fichier ne prouve ni le boot ROM
ni la persistance des paramètres sur carte.

Sans paramètres enregistrés ni configuration `-X` valide, rejoindre
`Moonraker-Setup` avec le mot de passe WPA2 unique de compilation, puis ouvrir
explicitement `http://192.168.4.1`. DHCP est actif ; pas de DNS captif automatique.
Le formulaire couvre Wi-Fi, hôte/port/clé API Moonraker, hôte/port/authentification
MQTT, identifiant client, préfixe des topics, polling et commandes optionnelles.
Les secrets passent dans un POST JSON borné, jamais dans une URL. Aucun serveur
d’administration sur le LAN ni restitution des secrets enregistrés.

**Sauvegarder = préparer en RAM RTC → redémarrer → écrire avant initialisation
radio**, jamais écrire la flash pendant le Wi-Fi. HTTP 202 signifie « préparé »,
pas « durable ». Le démarrage suivant affiche `SETTINGS: durable record loaded`
après vérification CRC et relecture. Les paramètres persistants remplacent les
anciens paramètres `-X` optionnels (`WiFiSSID`, `MoonrakerHost`, `MQTTHost`, etc.).
Sans mot de passe de portail, refus de démarrer un AP ouvert. Une recompilation
avec `ForceSetup=true` permet de revenir explicitement au portail sans effacer
les paramètres existants.
**Ce flag est permanent, pas à usage unique :** après sauvegarde, chaque reboot
revient encore au portail. Pour reprendre le pont, recompiler et réinstaller avec
`-X moonraker2mqtt/platform/esp32s3.ForceSetup=false` (ou omettre le flag), en
préservant les secteurs de paramètres pendant le remplacement du firmware.

Un stockage corrompu, une version inconnue ou des générations ambiguës bloquent
la sauvegarde normale (HTTP 409) et proposent une réparation distincte. Saisir
les paramètres de remplacement, cocher la confirmation d’effacement des deux
secteurs puis confirmer le dialogue du navigateur. Le POST `/repair`, authentifié
et borné, prépare une opération protégée par CRC ; seuls les deux secteurs
réservés sont effacés/réinitialisés au démarrage suivant. **Une version inconnue
n’est effacée qu’après cette confirmation.** Sauvegarder les preuves si nécessaire.
Les erreurs d’E/S ou de relecture n’autorisent pas la réparation : diagnostiquer
le stockage. HTTP 202 ne prouve pas la durabilité ; une réparation interrompue
peut perdre les deux anciens enregistrements. Aucun effacement ni nouvel essai
automatique.

### État de validation et limites

L'adaptation standard conserve les abonnements MQTT bornés, les payloads en
flux, les buffers de réception WebSocket empruntés, le décodage typé de l'état
Klippy avec repli en cas de collision, les workers réutilisables et la réservation
TCP native. Les connexions protocolaires passent par un adaptateur lneto
implémentant l'interface publique `net.Conn`, sans extension privée de `net`
TinyGo. Interruption de génération et retrait du stockage restent dans la
dépendance réseau ; le HTTP du portail utilise le `net` standard. L'adresse
locale non exposée est signalée inconnue (`nil`), pas inventée.

**La publication JSON possède maintenant toujours le résultat de `json.Marshal`
standard**, y compris sur MCU. La publication reste synchrone, mais réintroduit
la copie JSON finale et son allocation. Plus de bail privé d'encodeur ni de hooks
d'origine JSON/allocateur : ne pas attribuer à ce firmware les anciens gains
ou profils de producteurs. Le volume d'allocations et la pression de
fragmentation peuvent augmenter. Aucune nouvelle garantie de pic mémoire,
marge de pile ou stabilité MCU n'est apportée.

Un échec Wi-Fi au démarrage avec paramètres enregistrés demande toujours un
reboot contrôlé après le délai post-erreur existant de 30 secondes. L'appel
natif d'association lui-même n'est **ni annulable ni borné en durée**. La reprise
MQTT/Moonraker et le retrait natif restent distincts de ce reboot Wi-Fi : aucun
reboot MQTT de substitution ni worker de reprise abandonné. Comportement hôte
inchangé.

**Compiler sur MCU et qualifier physiquement sont deux étapes distinctes.**
L'observation historique d'une heure utilisait un compilateur modifié : elle
ne qualifie pas ce firmware standard. Les anciens chiffres flash/RAM statique
ne sont pas des mesures stock actuelles. Ni installation, trafic imprimante/broker,
écriture de paramètres ni essai physique ne sont autorisés ou impliqués par
les tests hôte ou une compilation réussie.

Deux secteurs de 4Kio sont réservés dans `[0x1fe000, 0x200000)`, volontairement
sous la géométrie ROM par défaut de 2Mio sur la vraie flash de 4Mio. Le linker
refuse le chevauchement de l’image ; aucun changement aveugle de géométrie.
La préparation RTC est `NOLOAD`, hors segments de l’image et effacement BSS.
C’est une preuve de placement statique, **pas encore un essai de rétention lors
d’un reset physique**. Avant tout accès, le driver vérifie puce/révision,
callbacks ROM, sécurité, cœur1 en reset et absence de mappings PSRAM. Tous les
accès flash sont définitivement bloqués pour ce démarrage avant la radio.
Wrapper et vecteurs d’exception temporaires utilisent uniquement IRAM/ROM.

Avant tout flashage, obtenir une autorisation explicite pour l'image et l'action
exactes. La qualification erase/program des paramètres exige aussi une
autorisation distincte ; ni effacement complet ni changement d'eFuse. L'ancien
avertissement SHA ROM indépendant n'est pas prétendu corrigé par cette adaptation.

MQTT TCP et `ws://` sans TLS ; QoS0 ; 4 abonnements exacts, payload MQTT 4096
octets, topics 256 octets, 4 commandes en file, 4 messages WebSocket en file,
8 requêtes JSON-RPC simultanées et trames WebSocket de 16Kio. Pas d’OTA, de
DNS captif automatique ni de qualification watchdog. L’initialisation Wi-Fi
n’est tentée qu’une fois par démarrage ; les échecs association/DHCP sont retentés
avec attente de 5–30 secondes, puis portail de secours après six échecs sans
effacer les paramètres. Deux emplacements TCP simultanés sont réservés à MQTT
et Moonraker ; la pompe réseau est jointe avant remplacement d’une pile ayant
échoué avant le bridge. Une perte d’association annule le bridge puis provoque
un redémarrage logiciel, au lieu de réutiliser les descripteurs alors que des
workers WebSocket peuvent subsister. Ce reset annule le RTC en attente et
préserve les paramètres durables. Tests de régression, deux sockets lneto réels
en mémoire et compilation avec le pilote épinglé passent ; reprise sur carte,
mémoire dynamique et tests prolongés restent non qualifiés. Aucun broker ni
imprimante de production contacté. Secrets en clair dans les artefacts et la
flash : garder les binaires privés et utiliser un LAN de confiance. Les limites
Linux ne changent pas.

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