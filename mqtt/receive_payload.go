//go:build tinygo || natiu

package mqtt

import (
	"errors"
	native "github.com/soypat/natiu-mqtt"
	"io"
)

// The native callback supplies a validated PUBLISH header and variable header.
// Its reader is limited to the payload. Validate the known length before
// allocation, then read into independently owned exact-size storage. ReadAll's
// chunk/final assembly grows a second backing store (8192 for a 4096-byte
// payload on the pinned TinyGo runtime). This removes that growth, NOT the
// remaining contiguous owned-payload allocation.
func readPublishPayload(h native.Header, v native.VariablesPublish, r io.Reader) ([]byte, error) {
	variableSize := uint64(v.Size(h.Flags().QoS()))
	if uint64(h.RemainingLength) < variableSize {
		return nil, errors.New("MQTT incoming payload has invalid remaining length")
	}
	n := uint64(h.RemainingLength) - variableSize
	if n > MaxPayload {
		return nil, errors.New("MQTT incoming payload exceeds limit")
	}
	data := make([]byte, int(n))
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}
	return data, nil
}
