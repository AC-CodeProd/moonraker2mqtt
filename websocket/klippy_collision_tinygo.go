//go:build tinygo || lowmem

package websocket

import (
	"bytes"
	"moonraker2mqtt/internal/diagjson"
	"strings"
)

// This scanner is ONLY called after encoding/json has validated the entire
// frame and identified a pending typed request. It is not a JSON validator.
// It visits envelope keys and direct result keys, skipping all other values
// iteratively: O(frame length), constant stack/storage, no per-node Tokens.
// No slice or key survives the call. Escaped keys use encoding/json unquoting
// (exceptional small owned allocations), never a full-frame RawMessage copy.
type klippyCollisionScan struct {
	data []byte
	pos  int
}

func (s *klippyCollisionScan) space() {
	for s.pos < len(s.data) {
		switch s.data[s.pos] {
		case ' ', '\t', '\r', '\n':
			s.pos++
		default:
			return
		}
	}
}
func (s *klippyCollisionScan) quoted() []byte {
	start := s.pos
	s.pos++
	for {
		switch s.data[s.pos] {
		case '\\':
			s.pos += 2
		case '"':
			s.pos++
			return s.data[start:s.pos]
		default:
			s.pos++
		}
	}
}

// 0 unrelated; 1 exact decoded name; 2 case-fold equivalent name. EqualFold
// includes Unicode simple folds (e.g. Kelvin sign and long s), like JSON's
// folded struct lookup. Canonical ASCII keys don't allocate owned strings.
func klippyKeyKind(raw []byte, target string) int {
	key := raw[1 : len(raw)-1]
	if bytes.IndexByte(key, '\\') >= 0 {
		var name string
		// Syntax was already validated; use the primary decoder's escaping rules.
		if diagjson.Unmarshal(105, raw, &name) != nil {
			return 0
		}
		if name == target {
			return 1
		}
		if strings.EqualFold(name, target) {
			return 2
		}
		return 0
	}
	if string(key) == target {
		return 1
	}
	if strings.EqualFold(string(key), target) {
		return 2
	}
	return 0
}
func (s *klippyCollisionScan) skipValue() {
	s.space()
	if s.data[s.pos] == '"' {
		s.quoted()
		return
	}
	if s.data[s.pos] != '{' && s.data[s.pos] != '[' {
		for s.pos < len(s.data) {
			switch s.data[s.pos] {
			case ',', '}', ']', ' ', '\t', '\r', '\n':
				return
			default:
				s.pos++
			}
		}
		return
	}
	depth := 0
	for {
		switch s.data[s.pos] {
		case '"':
			s.quoted()
			continue
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		}
		s.pos++
		if depth == 0 {
			return
		}
	}
}
func (s *klippyCollisionScan) resultCollision() bool {
	s.space()
	if s.data[s.pos] != '{' {
		s.skipValue()
		return false
	}
	s.pos++
	seen := false
	for {
		s.space()
		if s.data[s.pos] == '}' {
			s.pos++
			return false
		}
		kind := klippyKeyKind(s.quoted(), "klippy_state")
		s.space()
		s.pos++ // colon, guaranteed by the primary syntax validation
		if kind != 0 {
			// A folded alternative can change the map+Marshal sorted-key precedence,
			// even when its value is null. Duplicate exact keys can change null/type
			// semantics. Use the original roundtrip only for these exceptional shapes.
			if seen || kind == 2 {
				return true
			}
			seen = true
		}
		s.skipValue()
		s.space()
		if s.data[s.pos] == ',' {
			s.pos++
		}
	}
}
func klippyNeedsRoundtrip(data []byte) bool {
	s := klippyCollisionScan{data: data}
	s.space()
	s.pos++ // a pending ID entails an object; JSON validated upstream
	seen := false
	for {
		s.space()
		if s.data[s.pos] == '}' {
			return false
		}
		kind := klippyKeyKind(s.quoted(), "result")
		s.space()
		s.pos++
		if kind != 0 {
			// Generic Result:any replaces whole objects; *result merges duplicate
			// objects. Envelope spelling itself doesn't matter, only duplication does.
			if seen {
				return true
			}
			seen = true
			if s.resultCollision() {
				return true
			}
		} else {
			s.skipValue()
		}
		s.space()
		if s.data[s.pos] == ',' {
			s.pos++
		}
	}
}
func decodeKlippyRoundtrip(data []byte, value interface{}) error {
	var fallback WebSocketMessage
	if err := diagjson.Unmarshal(106, data, &fallback); err != nil {
		return err
	}
	// Preserve the original generic -> Marshal -> ServerInfo semantics, including
	// last exact key, sorted case-collisions and result replacement, on exceptions.
	owned, err := diagjson.Marshal(3, fallback.Result)
	var info ServerInfo
	if err == nil {
		err = diagjson.Unmarshal(107, owned, &info)
	}
	fallback.Result = &klippyStateResult{KlippyState: info.KlippyState, decodeErr: err}
	*value.(*WebSocketMessage) = fallback
	return nil
}
