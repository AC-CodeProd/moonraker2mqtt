package bridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
)

type ownedPublicationMQTT struct {
	recordingMQTT
	entered chan []byte
	resume  chan struct{}
	mode    string
}

func (m *ownedPublicationMQTT) Publish(_ string, p []byte, _ byte, _ bool, _ int) error {
	// Intentionally retain the actual slice, not a test-owned copy.
	m.entered <- p
	<-m.resume
	switch m.mode {
	case "error":
		return errors.New("offline publication failure")
	case "panic":
		panic("offline publication panic")
	}
	return nil
}

// No private json/bytes hooks: standard Marshal ownership is the oracle for
// both actual publication callers, including blocked concurrent publishers.
func TestOwnedPublication(t *testing.T) {
	for _, status := range []bool{false, true} {
		name := "notification"
		if status {
			name = "status"
		}
		t.Run(name, func(t *testing.T) {
			for _, mode := range []string{"success", "error", "panic"} {
				t.Run(mode, func(t *testing.T) {
					m := &ownedPublicationMQTT{recordingMQTT: recordingMQTT{connected: true}, entered: make(chan []byte, 2), resume: make(chan struct{}), mode: mode}
					a := New(config.DefaultConfig(), m, logger.NewSerial(io.Discard, logger.ERROR))
					type result struct {
						pan any
						ok  bool
					}
					done := make(chan result, 2)
					send := func(v string) {
						r := result{}
						defer func() { r.pan = recover(); done <- r }()
						if status {
							r.ok = a.publishStatusObject("owned", map[string]string{"v": v})
						} else {
							a.OnNotification("owned", map[string]string{"v": v})
						}
					}
					// Always release and join test workers, even after a failed assertion.
					started, joined := 0, 0
					t.Cleanup(func() {
						select {
						case <-m.resume:
						default:
							close(m.resume)
						}
						for joined < started {
							select {
							case <-done:
								joined++
							case <-time.After(time.Second):
								t.Error("publication cleanup did not join")
								return
							}
						}
					})
					get := func(want string) []byte {
						select {
						case p := <-m.entered:
							var decoded map[string]string
							if err := json.Unmarshal(p, &decoded); err != nil || decoded["v"] != want {
								t.Fatalf("publication payload: %q, decode error: %v", p, err)
							}
							return p
						case <-time.After(time.Second):
							t.Fatal("independent publication not entered")
							return nil
						}
					}
					started++
					go send(strings.Repeat("a", 1771))
					first := get(strings.Repeat("a", 1771))
					wantFirst := append([]byte(nil), first...)
					started++
					go send(strings.Repeat("b", 4094))
					second := get(strings.Repeat("b", 4094))
					wantSecond := append([]byte(nil), second...)
					for i := 0; i < 100; i++ {
						if _, err := json.Marshal(map[string]string{"churn": strings.Repeat("z", 4094)}); err != nil {
							t.Fatal(err)
						}
					}
					if !bytes.Equal(first, wantFirst) || !bytes.Equal(second, wantSecond) {
						t.Fatal("published owned bytes mutated during encoder churn")
					}
					select {
					case <-done:
						joined++
						t.Fatal("publication returned before Publish")
					default:
					}
					close(m.resume)
					for joined < started {
						select {
						case r := <-done:
							joined++
							if (r.pan != nil) != (mode == "panic") {
								t.Fatalf("unexpected panic: %v", r.pan)
							}
							if status && r.ok != (mode == "success") {
								t.Fatalf("status result %v for %s", r.ok, mode)
							}
						case <-time.After(time.Second):
							t.Fatal("publication did not return")
						}
					}
					for i := 0; i < 100; i++ {
						_, _ = json.Marshal(strings.Repeat("c", 4094))
					}
					if !bytes.Equal(first, wantFirst) || !bytes.Equal(second, wantSecond) {
						t.Fatal("release invalidated independently owned result")
					}
				})
			}
		})
	}
}

type ownedEncodingFailure struct{ panicOnMarshal bool }

func (v ownedEncodingFailure) MarshalJSON() ([]byte, error) {
	if v.panicOnMarshal {
		panic("offline encoder panic")
	}
	return nil, errors.New("offline encoder failure")
}

func TestOwnedPublicationEncodingFailure(t *testing.T) {
	for _, status := range []bool{false, true} {
		for _, panicOnMarshal := range []bool{false, true} {
			m := &recordingMQTT{connected: true}
			a := New(config.DefaultConfig(), m, logger.NewSerial(io.Discard, logger.ERROR))
			func() {
				defer func() {
					if pan := recover(); (pan != nil) != panicOnMarshal {
						t.Fatalf("unexpected encoder panic: %v", pan)
					}
				}()
				v := ownedEncodingFailure{panicOnMarshal: panicOnMarshal}
				if status {
					if a.publishStatusObject("owned", v) {
						t.Fatal("encoding failure reported success")
					}
				} else {
					a.OnNotification("owned", v)
				}
			}()
			if len(m.messages) != 0 {
				t.Fatal("published despite encoding failure")
			}
			// Error/panic must not poison subsequent ordinary publication.
			if !a.publishStatusObject("next", map[string]int{"value": 1}) || len(m.messages) != 1 {
				t.Fatal("subsequent owned publication failed")
			}
		}
	}
}
