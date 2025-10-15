package websocket

import "encoding/json"

func (e *RPCError) Error() string {
	return e.Message
}

func (m *WebSocketMessage) MarshalJSON() ([]byte, error) {
	type Alias WebSocketMessage
	return json.Marshal(&struct {
		*Alias
		JSONRPC string `json:"jsonrpc"`
	}{
		Alias:   (*Alias)(m),
		JSONRPC: "2.0",
	})
}

func (m *WebSocketMessage) IsResponse() bool {
	return m.ID != nil
}

func (m *WebSocketMessage) IsNotification() bool {
	return m.Method != "" && m.ID == nil
}

func (r *WebSocketResponse) IsError() bool {
	return r.Error != nil
}

func NewWebSocketRequest(id int, method string, params any, timeout int) *WebSocketRequest {
	return &WebSocketRequest{
		ID:       id,
		Method:   method,
		Params:   params,
		Response: make(chan WebSocketResponse, 1),
		timeout:  timeout,
	}
}
