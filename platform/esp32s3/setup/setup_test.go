package setup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"moonraker2mqtt/platform/esp32s3/store"
	"net"
	"strings"
	"testing"
	"time"
)

func good() Settings {
	return Settings{SSID: "lab", WiFiPassword: "lab-password", MoonrakerHost: "printer.local", MoonrakerPort: 7125, APIKey: "SECRET-API", MQTTHost: "broker.local", MQTTPort: 1883, MQTTUsername: "test", MQTTPassword: "SECRET-PASS", ClientID: "s3-lab", TopicPrefix: "moonraker", PollSeconds: 5}
}
func payload() []byte { b, _ := json.Marshal(good()); return b }
func TestValidation(t *testing.T) {
	for _, change := range []func(*Settings){func(s *Settings) { s.SSID = strings.Repeat("s", 33) }, func(s *Settings) { s.WiFiPassword = "short" }, func(s *Settings) { s.MoonrakerHost = "http://evil" }, func(s *Settings) { s.MQTTPort = 65536 }, func(s *Settings) { s.MQTTUsername = "" }, func(s *Settings) { s.TopicPrefix = "a/#" }, func(s *Settings) { s.PollSeconds = 0 }, func(s *Settings) { s.APIKey = "a\nb" }, func(s *Settings) { s.MQTTPassword = strings.Repeat("a", 257) }} {
		s := good()
		change(&s)
		if s.Validate() == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
	s, e := Decode(payload())
	if e != nil || s.Config().Validate() != nil {
		t.Fatalf("valid: %v", e)
	}
	for _, b := range [][]byte{[]byte("{}"), append(payload(), []byte("{}")...), []byte(`{"unknown":true}`), bytes.Repeat([]byte("x"), MaxSettings+1)} {
		if _, e := Decode(b); e == nil {
			t.Fatal("invalid JSON accepted")
		}
	}
}
func TestPending(t *testing.T) {
	for _, soft := range []bool{false, true} {
		b := make([]byte, PendingSize)
		if e := Stage(b, payload()); e != nil {
			t.Fatal(e)
		}
		p, e := Take(b, soft)
		if e != nil || (p != nil) != soft {
			t.Fatal("reset selection")
		}
		p, e = Take(b, true)
		if e != nil || p != nil {
			t.Fatal("pending replay")
		}
	}
	for _, i := range []int{0, 4, 8, 16, 40} {
		b := make([]byte, PendingSize)
		Stage(b, payload())
		b[i] ^= 128
		p, _ := Take(b, true)
		if p != nil {
			t.Fatal("torn handoff accepted")
		}
	}
}
func exchange(t *testing.T, p Portal, raw string) (string, bool) {
	t.Helper()
	server, client := net.Pipe()
	done := make(chan bool, 1)
	go func() { done <- p.Handle(server) }()
	client.SetDeadline(time.Now().Add(time.Second))
	go func() { io.WriteString(client, raw) }()
	b, e := io.ReadAll(client)
	client.Close()
	if e != nil {
		t.Fatal(e)
	}
	return string(b), <-done
}
func post(body string) string {
	return fmt.Sprintf("POST /settings HTTP/1.1\r\nHost: 192.168.4.1\r\nOrigin: http://192.168.4.1\r\nContent-Type: application/json\r\nX-Setup-Token: token\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
}
func TestPortalSecurityAndBounds(t *testing.T) {
	calls := 0
	p := Portal{Token: "token", Page: Page, Save: func(b []byte) error { calls++; return nil }}
	tests := []struct {
		raw  string
		code string
	}{
		{"GET / HTTP/1.1\r\nHost: 192.168.4.1\r\n\r\n", "200"},
		{post(string(payload())), "202"},
		{strings.Replace(post(string(payload())), "token", "wrong", 1), "403"},
		{strings.Replace(post(string(payload())), "Origin: http://192.168.4.1", "Origin: http://evil.local", 1), "403"},
		{strings.Replace(post(string(payload())), "Host: 192.168.4.1", "Host: evil.local", 1), "400"},
		{post("{}"), "422"},
		{post(strings.Repeat("x", MaxSettings+1)), "413"},
		{strings.Replace(post(string(payload())), "Content-Length:", "Transfer-Encoding: chunked\r\nContent-Length:", 1), "400"},
		{strings.Replace(post(string(payload())), "Content-Length:", "Content-Length: 1\r\nContent-Length:", 1), "400"},
		{"GET /settings?password=SECRET HTTP/1.1\r\nHost: 192.168.4.1\r\n\r\n", "404"},
		{"GET / HTTP/1.1\r\nHost: 192.168.4.1\r\nX-Fill: " + strings.Repeat("a", MaxHeaders) + "\r\n\r\n", "400"},
	}
	for _, tt := range tests {
		response, accepted := exchange(t, p, tt.raw)
		if !strings.HasPrefix(response, "HTTP/1.1 "+tt.code) {
			t.Fatalf("code %s: %.100s", tt.code, response)
		}
		if accepted != (tt.code == "202") {
			t.Fatal("reboot acceptance")
		}
		if strings.Contains(response, "SECRET") {
			t.Fatal("secret reflected")
		}
	}
	if calls != 1 {
		t.Fatalf("save calls %d", calls)
	}
}

type nor struct {
	data   [2][store.SectorSize]byte
	radio  bool
	writes int
}

func newNOR() *nor {
	n := new(nor)
	for i := range n.data {
		for j := range n.data[i] {
			n.data[i][j] = 255
		}
	}
	return n
}
func (n *nor) Read(s, o int, b []byte) error {
	if n.radio {
		panic("flash while radio live")
	}
	copy(b, n.data[s][o:])
	return nil
}
func (n *nor) Erase(s int) error {
	if n.radio {
		panic("flash while radio live")
	}
	for i := range n.data[s] {
		n.data[s][i] = 255
	}
	n.writes++
	return nil
}
func (n *nor) Program(s, o int, b []byte) error {
	if n.radio {
		panic("flash while radio live")
	}
	for i, v := range b {
		if n.data[s][o+i]&v != v {
			panic("NOR violation")
		}
		n.data[s][o+i] &= v
	}
	n.writes++
	return nil
}
func TestPortalRebootDurableLifecycle(t *testing.T) {
	n := newNOR()
	retained := make([]byte, PendingSize)
	n.radio = true
	p := Portal{Token: "token", Save: func(b []byte) error { return Stage(retained, b) }}
	_, accepted := exchange(t, p, post(string(payload())))
	if !accepted || n.writes != 0 {
		t.Fatal("portal wrote flash")
	}
	// Simulated software reset: retention survives, radio becomes inactive.
	n.radio = false
	b, e := Take(retained, true)
	if e != nil {
		t.Fatal(e)
	}
	s := store.Store{Flash: n}
	if e = s.Save(b); e != nil {
		t.Fatal(e)
	}
	r, e := s.Load()
	if e != nil {
		t.Fatal(e)
	}
	settings, e := Decode(r.Payload)
	if e != nil || settings.SSID != "lab" || settings.Config().MQTT.CommandsEnabled {
		t.Fatal("boot config")
	}
	n.radio = true // writes now forbidden; next cold boot loads durable storage.
	n.radio = false
	lost := make([]byte, PendingSize)
	pending, e := Take(lost, false)
	if e != nil || pending != nil {
		t.Fatal("cold boot pending")
	}
	r, e = s.Load()
	if e != nil || !bytes.Equal(r.Payload, payload()) {
		t.Fatal("cold boot durability")
	}
}
