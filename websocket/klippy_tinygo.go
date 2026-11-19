//go:build tinygo || lowmem

package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"moonraker2mqtt/internal/diagjson"
	wire "moonraker2mqtt/websocket/wire/lowmem"
	"strings"
	"time"
)

// Ordinary encoding/json schema: no custom decoder, RawMessage, or aliases.
type klippyStateResult struct {
	KlippyState string `json:"klippy_state"`
	decodeErr   error
}
type klippyStateEnvelope struct {
	JSONRPC string             `json:"jsonrpc"`
	Method  string             `json:"method,omitempty"`
	Params  any                `json:"params,omitempty"`
	ID      *int               `json:"id,omitempty"`
	Result  *klippyStateResult `json:"result,omitempty"`
	Error   *RPCError          `json:"error,omitempty"`
}

func (c *WebSocketClient) RequestKlippyState(ctx context.Context) (string, error) {
	// Match Request's existing preflight-only cancellation (including nil ctx).
	if ctx.Err() != nil {
		return "", fmt.Errorf("request cancelled: %w", ctx.Err())
	}
	message, err := c.requestReusable(ctx, "server.info", nil, 30*time.Second, true)
	if err != nil {
		return "", err
	}
	state, ok := message.Result.(*klippyStateResult)
	if !ok {
		return "", NewWebSocketError("invalid typed state response", nil)
	}
	if state == nil {
		return "", nil
	} // null result matches the original zero ServerInfo.
	return state.KlippyState, state.decodeErr
}
func (c *WebSocketClient) decodeMessage(data []byte, _ byte, value interface{}) error {
	return c.decodeMessageProof(data, 0, value, wire.FrameProof{})
}
func (c *WebSocketClient) decodeMessageProof(data []byte, _ byte, value interface{}, proof wire.FrameProof) error {
	// Peek only the ID: unknown JSON values are validated/skipped, not copied or
	// decoded into maps. The borrowed frame remains locked through both passes.
	var envelope struct {
		ID *int `json:"id"`
	}
	proof.ObserveFirst(data)
	if err := diagjson.Unmarshal(101, data, &envelope); err != nil {
		proof.EmitFailure()
		return err
	}
	typed := false
	if envelope.ID != nil {
		c.requestsMux.RLock()
		request := c.requests[*envelope.ID]
		typed = request != nil && request.klippyState
		c.requestsMux.RUnlock()
	}
	if !typed {
		return diagjson.Unmarshal(102, data, value)
	}
	if klippyNeedsRoundtrip(data) {
		return decodeKlippyRoundtrip(data, value)
	}
	var decoded klippyStateEnvelope
	if err := diagjson.Unmarshal(103, data, &decoded); err != nil {
		var typeErr *json.UnmarshalTypeError
		if !errors.As(err, &typeErr) || (typeErr.Field != "result" && !strings.HasPrefix(typeErr.Field, "result.")) {
			return err
		}
		// Valid JSON of the wrong result shape used to fail at GetServerInfo,
		// not tear down RX and wait for timeout. Error-only generic fallback
		// validates and owns envelope metadata; it never converts a result map.
		var fallback WebSocketMessage
		if envelopeErr := diagjson.Unmarshal(104, data, &fallback); envelopeErr != nil {
			return envelopeErr
		}
		fallback.Result = &klippyStateResult{decodeErr: err}
		*value.(*WebSocketMessage) = fallback
		return nil
	}
	*value.(*WebSocketMessage) = WebSocketMessage{JSONRPC: decoded.JSONRPC, Method: decoded.Method, Params: decoded.Params, ID: decoded.ID, Result: decoded.Result, Error: decoded.Error}
	return nil
}
func (c *WebSocketClient) receiveMessage(conn *wire.Conn, message *WebSocketMessage) error {
	moonrakerReceiveStorage.Lock()
	defer moonrakerReceiveStorage.Unlock()
	codec := wire.Codec{Marshal: wire.JSON.Marshal, Unmarshal: c.decodeMessage}
	if wire.FrameProofDiagnostics == "true" {
		codec.UnmarshalProof = c.decodeMessageProof
	}
	// Both schemas are fresh and all decoded strings/maps/slices are owned after
	// return. Late/deleted IDs and all other RPCs use the untouched generic schema.
	return codec.ReceiveBorrowed(conn, message, moonrakerReceiveStorage.payload[:])
}
