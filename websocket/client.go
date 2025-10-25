package websocket

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"sync"
	"time"

	"golang.org/x/net/websocket"

	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
)

type WebSocketClient struct {
	config       *config.MoonrakerConfig
	listener     StatusListener
	conn         *websocket.Conn
	state        string
	stateMux     sync.RWMutex
	requests     map[int]*WebSocketRequest
	requestsMux  sync.RWMutex
	nextID       int
	sendChan     chan *WebSocketMessage
	closeChan    chan struct{}
	dataHandlers []DataHandler
	handlersMux  sync.RWMutex
	retry        *Retry
	logger       logger.Logger
}

func NewWebSocketClient(config *config.MoonrakerConfig, listener StatusListener, logger logger.Logger) *WebSocketClient {

	return &WebSocketClient{
		config:       config,
		listener:     listener,
		state:        WEB_SOCKET_STATE_STOPPED,
		requests:     make(map[int]*WebSocketRequest),
		nextID:       1,
		sendChan:     make(chan *WebSocketMessage, 100),
		closeChan:    make(chan struct{}),
		dataHandlers: make([]DataHandler, 0),
		retry:        NewRetry(config.AutoReconnect, config.MaxReconnectAttempts, logger),
		logger:       logger,
	}
}

func (c *WebSocketClient) Connect(ctx context.Context) error {
	err := c.connectOnce(ctx)
	if err == nil {
		c.retry.Reset()
		return nil
	}

	if !c.retry.IsEnabled() {
		return err
	}

	go c.reconnectLoop(ctx)
	return nil
}

func (c *WebSocketClient) connectOnce(ctx context.Context) error {
	c.stateMux.Lock()
	defer c.stateMux.Unlock()

	if c.state != WEB_SOCKET_STATE_STOPPED {
		return NewClientAlreadyConnectedError("client already connecting or connected")
	}

	c.setState(WEB_SOCKET_STATE_CONNECTING)

	wsURL := c.config.GetWebSocketURL()

	url, err := url.Parse(wsURL)
	if err != nil {
		c.setState(WEB_SOCKET_STATE_STOPPED)
		return fmt.Errorf("invalid WebSocket URL: %w", err)
	}

	wsConfig, err := websocket.NewConfig(wsURL, "http://"+url.Host)
	if err != nil {
		c.setState(WEB_SOCKET_STATE_STOPPED)
		return fmt.Errorf("failed to create WebSocket config: %w", err)
	}

	if c.config.APIKey != "" {
		wsConfig.Header = http.Header{}
		wsConfig.Header.Set("X-Api-Key", c.config.APIKey)
	}

	type result struct {
		conn *websocket.Conn
		err  error
	}
	resultChan := make(chan result, 1)

	go func() {
		conn, err := websocket.DialConfig(wsConfig)
		resultChan <- result{conn: conn, err: err}
	}()

	select {
	case <-ctx.Done():
		c.setState(WEB_SOCKET_STATE_STOPPED)
		return fmt.Errorf("connection cancelled: %w", ctx.Err())
	case res := <-resultChan:
		if res.err != nil {
			c.setState(WEB_SOCKET_STATE_STOPPED)
			if c.listener != nil {
				c.listener.OnException(NewWebSocketError("connection failed", res.err))
			}
			return fmt.Errorf("failed to connect to WebSocket: %w", res.err)
		}

		c.conn = res.conn
		c.setState(WEB_SOCKET_STATE_CONNECTED)

		c.sendChan = make(chan *WebSocketMessage, 100)
		c.closeChan = make(chan struct{})

		go c.readLoop()
		go c.writeLoop()

		c.logger.Info("Connected to Moonraker at %s", wsURL)
		return nil
	}
}

func (c *WebSocketClient) reconnectLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			if !c.retry.ShouldReconnect() {
				return
			}

			if err := c.retry.WaitBeforeReconnect(ctx); err != nil {
				return
			}

			if ctx.Err() != nil {
				return
			}

			err := c.connectOnce(ctx)
			if err == nil {
				c.logger.Info("WebSocket reconnection successful")
				c.retry.Reset()
				return
			}

			c.logger.Error("WebSocket reconnection failed: %v", err)
		}
	}
}

func (c *WebSocketClient) Disconnect() error {
	c.stateMux.Lock()
	defer c.stateMux.Unlock()

	if c.state == WEB_SOCKET_STATE_STOPPED {
		return nil
	}

	c.setState(WEB_SOCKET_STATE_STOPPING)

	select {
	case <-c.closeChan:
	default:
		close(c.closeChan)
	}

	if c.conn != nil {
		if closeErr := c.conn.Close(); closeErr != nil {
			c.logger.Debug("WebSocket connection closed during shutdown: %v", closeErr)
		}
		c.conn = nil
	}

	c.setState(WEB_SOCKET_STATE_STOPPED)
	c.logger.Info("Disconnected from Moonraker")
	return nil
}

func (c *WebSocketClient) GetState() string {
	c.stateMux.RLock()
	defer c.stateMux.RUnlock()
	return c.state
}

func (c *WebSocketClient) IsConnected() bool {
	return c.GetState() == WEB_SOCKET_STATE_CONNECTED
}

func (c *WebSocketClient) setState(newState string) {
	c.state = newState

	if c.listener != nil {
		c.listener.OnStateChanged(newState)
	}
}

func (c *WebSocketClient) RegisterDataHandler(handler DataHandler) {
	c.AddDataHandler(handler)
}

func (c *WebSocketClient) UnregisterDataHandler(handler DataHandler) {
	c.removeDataHandler(handler)
}

func (c *WebSocketClient) Request(ctx context.Context, method string, params any) (*WebSocketResponse, error) {
	if ctx.Err() != nil {
		return nil, fmt.Errorf("request cancelled: %w", ctx.Err())
	}

	msg, err := c.SendRequest(method, params)
	if err != nil {
		return nil, err
	}

	response := &WebSocketResponse{
		Result: msg.Result,
		Error:  msg.Error,
	}

	return response, nil
}
func (c *WebSocketClient) AddDataHandler(handler DataHandler) {
	c.handlersMux.Lock()
	defer c.handlersMux.Unlock()
	c.dataHandlers = append(c.dataHandlers, handler)
}

func (c *WebSocketClient) removeDataHandler(handler DataHandler) {
	c.handlersMux.Lock()
	defer c.handlersMux.Unlock()

	for i, h := range c.dataHandlers {
		if h == handler {
			c.dataHandlers = append(c.dataHandlers[:i], c.dataHandlers[i+1:]...)
			break
		}
	}
}

func (c *WebSocketClient) readLoop() {
	defer func() {
		if r := recover(); r != nil {
			c.logger.Error("Read loop panic: %v", r)
		}
	}()

	for {
		select {
		case <-c.closeChan:
			return
		default:
			var message WebSocketMessage
			err := websocket.JSON.Receive(c.conn, &message)
			if err != nil {
				if err == io.EOF {
					c.logger.Info("Connection closed by server")
				} else {
					if c.GetState() == WEB_SOCKET_STATE_STOPPING || c.GetState() == WEB_SOCKET_STATE_STOPPED {
						c.logger.Debug("Read error during shutdown (expected): %v", err)
					} else {
						c.logger.Error("Read error: %v, State: %s", err, c.GetState())
						if c.listener != nil {
							c.listener.OnException(NewWebSocketError("read error", err))
						}
					}
				}
				c.setState(WEB_SOCKET_STATE_STOPPED)
				return
			}

			c.handleMessage(&message)
		}
	}
}

func (c *WebSocketClient) writeLoop() {
	defer func() {
		if r := recover(); r != nil {
			c.logger.Error("Write loop panic: %v", r)
		}
	}()

	for {
		select {
		case <-c.closeChan:
			return
		case message := <-c.sendChan:
			err := websocket.JSON.Send(c.conn, message)
			if err != nil {
				c.logger.Error("Write error: %v", err)
				c.setState(WEB_SOCKET_STATE_STOPPED)
				if c.listener != nil {
					c.listener.OnException(NewWebSocketError("write error", err))
				}
				return
			}
		}
	}
}

func (c *WebSocketClient) handleMessage(message *WebSocketMessage) {
	if message.ID != nil {
		c.requestsMux.Lock()
		req, exists := c.requests[*message.ID]
		if exists {
			delete(c.requests, *message.ID)
		}
		c.requestsMux.Unlock()

		if exists {
			response := WebSocketResponse{
				Result: message.Result,
				Error:  message.Error,
			}
			req.Response <- response
			close(req.Response)
		}
		return
	}

	if message.Method != "" {
		c.handlersMux.RLock()
		handlers := make([]DataHandler, len(c.dataHandlers))
		copy(handlers, c.dataHandlers)
		c.handlersMux.RUnlock()

		for _, handler := range handlers {
			go handler.ProcessDataMessage(message)
		}

		if c.listener != nil {
			c.listener.OnNotification(message.Method, message.Params)
		}
	}
}

func (c *WebSocketClient) SendRequest(method string, params any) (*WebSocketMessage, error) {
	return c.SendRequestWithTimeout(method, params, 30*time.Second)
}

func (c *WebSocketClient) SendRequestWithTimeout(method string, params any, timeout time.Duration) (*WebSocketMessage, error) {
	if c.GetState() != WEB_SOCKET_STATE_CONNECTED {
		return nil, NewWebSocketNotConnectedError("not connected")
	}

	c.requestsMux.Lock()
	id := c.nextID
	c.nextID++

	req := &WebSocketRequest{
		ID:       id,
		Method:   method,
		Params:   params,
		Response: make(chan WebSocketResponse, 1),
	}
	c.requests[id] = req
	c.requestsMux.Unlock()

	message := &WebSocketMessage{
		ID:      &id,
		Method:  method,
		Params:  params,
		JSONRPC: "2.0",
	}
	select {
	case c.sendChan <- message:
	default:
		c.requestsMux.Lock()
		delete(c.requests, id)
		c.requestsMux.Unlock()
		return nil, NewWebSocketError("send buffer full", nil)
	}

	select {
	case response := <-req.Response:
		if response.Error != nil {
			return nil, response.Error
		}
		return &WebSocketMessage{
			Result:  response.Result,
			JSONRPC: "2.0",
		}, nil
	case <-time.After(timeout):
		c.requestsMux.Lock()
		delete(c.requests, id)
		c.requestsMux.Unlock()
		return nil, NewWebSocketTimeoutError("request timeout")
	}
}

func (c *WebSocketClient) Subscribe(objects map[string]any) error {
	_, err := c.SendRequest("server.websocket.subscribe", map[string]any{
		"objects": objects,
	})
	return err
}

func (c *WebSocketClient) SendNotification(method string, params any) error {
	if c.GetState() != WEB_SOCKET_STATE_CONNECTED {
		return NewWebSocketNotConnectedError("not connected")
	}

	message := &WebSocketMessage{
		Method:  method,
		Params:  params,
		JSONRPC: "2.0",
	}

	select {
	case c.sendChan <- message:
		return nil
	default:
		return NewWebSocketError("send buffer full", nil)
	}
}

func (c *WebSocketClient) EstablishConnection(ctx context.Context, maxRetries int) error {
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		err := c.Connect(ctx)
		if err == nil {
			return nil
		}

		lastErr = err
		c.logger.Info("Connection attempt %d/%d failed: %v", i+1, maxRetries, err)

		if i < maxRetries-1 {
			backoff := time.Duration(rand.Intn(1000)) * time.Millisecond
			if i > 0 {
				backoff = time.Duration(1<<uint(i)) * time.Second
			}

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}
	}

	return lastErr
}
