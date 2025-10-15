package websocket

import (
	"moonraker2mqtt/logger"
	"time"
)

const (
	WEB_SOCKET_STATE_CONNECTING = "ws_connecting"
	WEB_SOCKET_STATE_CONNECTED  = "ws_connected"
	WEB_SOCKET_STATE_STOPPING   = "ws_stopping"
	WEB_SOCKET_STATE_STOPPED    = "ws_stopped"
	INITIAL_RETRY_DELAY         = 1
	MAX_RETRY_DELAY             = 60
	RETRY_BACKOFF_MULTIPLIER    = 2
)

type WebSocketMessage struct {
	JSONRPC string    `json:"jsonrpc"`
	Method  string    `json:"method,omitempty"`
	Params  any       `json:"params,omitempty"`
	ID      *int      `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *RPCError `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type WebSocketRequest struct {
	ID       int
	Method   string
	Params   any
	Response chan WebSocketResponse
	timeout  int
}

type WebSocketResponse struct {
	Result any
	Error  *RPCError
}

type Notification struct {
	Method string `json:"method"`
	Params any    `json:"params"`
}

type ServerInfo struct {
	KlippyConnected       bool     `json:"klippy_connected"`
	KlippyState           string   `json:"klippy_state"`
	Components            []string `json:"components"`
	FailedComponents      []string `json:"failed_components"`
	RegisteredDirectories []string `json:"registered_directories"`
	Warnings              []string `json:"warnings"`
	WebsocketCount        int      `json:"websocket_count"`
	MoonrakerVersion      string   `json:"moonraker_version"`
}

type PrinterInfo struct {
	StateMessage    string         `json:"state_message"`
	KlippyPath      string         `json:"klipper_path"`
	PythonPath      string         `json:"python_path"`
	LogFile         string         `json:"log_file"`
	ConfigFile      string         `json:"config_file"`
	SoftwareVersion string         `json:"software_version"`
	Hostname        string         `json:"hostname"`
	CPUInfo         string         `json:"cpu_info"`
	Objects         map[string]any `json:"objects"`
}

type Retry struct {
	enabled      bool
	maxAttempts  int
	currentDelay time.Duration
	attempt      int
	logger       logger.Logger
}
