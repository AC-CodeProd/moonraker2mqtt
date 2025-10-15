package moonraker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
	"moonraker2mqtt/websocket"
)

type Listener interface {
	OnStateChanged(state string)
	OnNotification(method string, params any)
	OnException(err error)
}

type Client struct {
	wsClient websocket.Client
	listener Listener
	logger   logger.Logger
}

type CommandMessage struct {
	Command string                 `json:"command"`
	Params  map[string]interface{} `json:"params"`
}

type clientListener struct {
	parent Listener
}

func NewClient(config *config.MoonrakerConfig, logger logger.Logger, listener Listener) *Client {

	wsClient := websocket.NewWebSocketClient(config, &clientListener{
		parent: listener,
	}, logger)

	return &Client{
		wsClient: wsClient,
		listener: listener,
		logger:   logger,
	}
}

func (c *Client) Connect(ctx context.Context) error {
	return c.wsClient.Connect(ctx)
}

func (c *Client) Disconnect() error {
	return c.wsClient.Disconnect()
}

func (c *Client) IsConnected() bool {
	return c.wsClient.IsConnected()
}

func (c *Client) GetState() string {
	return c.wsClient.GetState()
}

func (c *Client) CallMethod(ctx context.Context, method string, params any) (any, error) {
	response, err := c.wsClient.Request(ctx, method, params)
	if err != nil {
		return nil, err
	}

	if response.IsError() {
		return nil, &websocket.RPCError{
			Code:    response.Error.Code,
			Message: response.Error.Message,
			Data:    response.Error.Data,
		}
	}

	return response.Result, nil
}

func (c *Client) GetHostInfo(ctx context.Context) (*websocket.PrinterInfo, error) {
	result, err := c.CallMethod(ctx, "printer.info", nil)
	if err != nil {
		return nil, err
	}

	data, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}

	var info websocket.PrinterInfo
	err = json.Unmarshal(data, &info)
	if err != nil {
		return nil, err
	}

	return &info, nil
}

func (c *Client) GetServerInfo(ctx context.Context) (*websocket.ServerInfo, error) {
	result, err := c.CallMethod(ctx, "server.info", nil)
	if err != nil {
		return nil, err
	}

	data, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}

	var info websocket.ServerInfo
	err = json.Unmarshal(data, &info)
	if err != nil {
		return nil, err
	}

	return &info, nil
}

func (c *Client) GetKlippyState(ctx context.Context) (string, error) {
	info, err := c.GetServerInfo(ctx)
	if err != nil {
		return "", err
	}

	return info.KlippyState, nil
}

func (c *Client) GetSupportedObjects(ctx context.Context) ([]string, error) {
	result, err := c.CallMethod(ctx, "printer.objects.list", nil)
	if err != nil {
		return nil, err
	}

	resultMap, ok := result.(map[string]any)
	if !ok {
		return nil, websocket.NewWebSocketError("invalid response format", nil)
	}

	objects, ok := resultMap["objects"].([]any)
	if !ok {
		return nil, websocket.NewWebSocketError("invalid objects format", nil)
	}

	var objectNames []string
	for _, obj := range objects {
		if name, ok := obj.(string); ok {
			objectNames = append(objectNames, name)
		}
	}

	return objectNames, nil
}

func (c *Client) QueryObjects(ctx context.Context, objects map[string]any) (map[string]any, error) {
	params := map[string]any{
		"objects": objects,
	}

	result, err := c.CallMethod(ctx, "printer.objects.query", params)
	if err != nil {
		return nil, err
	}

	resultMap, ok := result.(map[string]any)
	if !ok {
		return nil, websocket.NewWebSocketError("invalid response format", nil)
	}

	return resultMap, nil
}

func (c *Client) RestartPrinter(ctx context.Context) error {
	_, err := c.CallMethod(ctx, "printer.restart", nil)
	return err
}

func (c *Client) EmergencyStop(ctx context.Context) error {
	_, err := c.CallMethod(ctx, "printer.emergency_stop", nil)
	return err
}

func (c *Client) RestartFirmware(ctx context.Context) error {
	_, err := c.CallMethod(ctx, "printer.firmware_restart", nil)
	return err
}

func (c *Client) GetWebsocketID(ctx context.Context) (any, error) {
	return c.CallMethod(ctx, "server.websocket.id", nil)
}

func (c *Client) ExecuteGcode(ctx context.Context, gcode string) error {
	params := map[string]any{
		"script": gcode,
	}
	_, err := c.CallMethod(ctx, "printer.gcode.script", params)
	return err
}

func (c *Client) HandleCommand(topic string, payload []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c.logger.Info("Received command on topic: %s", topic)

	var cmdMsg CommandMessage
	if err := json.Unmarshal(payload, &cmdMsg); err != nil {
		c.logger.Error("Failed to parse command message: %v", err)
		return
	}

	if err := c.executeCommand(ctx, cmdMsg.Command, cmdMsg.Params); err != nil {
		c.logger.Error("Failed to execute command %s: %v", cmdMsg.Command, err)
	} else {
		c.logger.Info("Successfully executed command: %s", cmdMsg.Command)
	}
}

func (c *Client) executeCommand(ctx context.Context, command string, params map[string]interface{}) error {
	switch command {
	case "gcode":
		return c.handleGcodeCommand(ctx, params)
	case "emergency_stop":
		return c.EmergencyStop(ctx)
	case "restart":
		return c.RestartPrinter(ctx)
	case "firmware_restart":
		return c.RestartFirmware(ctx)
	default:
		return fmt.Errorf("unknown command: %s", command)
	}
}

func (c *Client) handleGcodeCommand(ctx context.Context, params map[string]interface{}) error {
	script, ok := params["script"].(string)
	if !ok {
		return fmt.Errorf("missing or invalid 'script' parameter")
	}
	return c.ExecuteGcode(ctx, script)
}

func (l *clientListener) OnStateChanged(state string) {
	if l.parent != nil {
		l.parent.OnStateChanged(state)
	}
}

func (l *clientListener) OnNotification(method string, params any) {
	if l.parent != nil {
		l.parent.OnNotification(method, params)
	}
}

func (l *clientListener) OnException(err error) {
	if l.parent != nil {
		l.parent.OnException(err)
	}
}
