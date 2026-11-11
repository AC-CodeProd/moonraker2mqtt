//go:build !tinygo && !jsoncontexttest
package diagjson
import "encoding/json"
func Unmarshal(class uintptr, data []byte, v any) error { return json.Unmarshal(data,v) }
