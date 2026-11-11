//go:build tinygo || jsoncontexttest

package diagjson

import "encoding/json"

func Marshal(producer uintptr, v any) ([]byte, error) { return json.MarshalOrigin(v, producer) }
