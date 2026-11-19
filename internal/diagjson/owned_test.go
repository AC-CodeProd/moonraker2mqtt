package diagjson

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestOwnedMarshalAndRelease(t *testing.T) {
	for _, status := range []bool{false, true} {
		var n Notification
		encode := n.Encode
		if status {
			encode = n.EncodeStatus
		}
		if err := encode(map[string]string{"value": strings.Repeat("a", 4094)}, true); err != nil {
			t.Fatal(err)
		}
		retained := n.Bytes()
		want := append([]byte(nil), retained...)
		n.Release()
		n.Release()
		if n.Bytes() != nil {
			t.Fatal("release kept owner's reference")
		}
		for i := 0; i < 100; i++ {
			if err := encode(map[string]string{"other": strings.Repeat("b", 8192)}, false); err != nil {
				t.Fatal(err)
			}
			n.Release()
		}
		if !bytes.Equal(retained, want) {
			t.Fatal("release or reuse mutated caller-retained stock Marshal bytes")
		}
		if err := encode(make(chan int), false); err == nil || n.Bytes() != nil {
			t.Fatal("unsupported value must fail without exposing prior bytes")
		}
	}
}

func TestUnmarshalOwnsDecodedValues(t *testing.T) {
	input := []byte(`{"text":"retained","raw":{"n":1},"bytes":"YWJj"}`)
	var out struct {
		Text string          `json:"text"`
		Raw  json.RawMessage `json:"raw"`
		Data []byte          `json:"bytes"`
	}
	if err := Unmarshal(1, input, &out); err != nil {
		t.Fatal(err)
	}
	for i := range input {
		input[i] = 'x'
	}
	if out.Text != "retained" || string(out.Raw) != `{"n":1}` || string(out.Data) != "abc" {
		t.Fatalf("decoded values alias reused receive storage: %+v", out)
	}
	if err := Unmarshal(1, []byte(`{"text":`), &out); err == nil {
		t.Fatal("malformed JSON accepted")
	}
}
