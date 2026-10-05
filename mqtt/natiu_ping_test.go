//go:build natiu && !tinygo

package mqtt

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

type pingLog struct {
	mu sync.Mutex
	bytes.Buffer
}

func (l *pingLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.Buffer.Write(p)
}

func (l *pingLog) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.Buffer.String()
}

func pingRegressionClient(t *testing.T, output io.Writer, broker func(net.Conn)) *NatiuClient {
	t.Helper()
	c := NewNatiuClient(config.DefaultConfig().MQTT, logger.NewSerial(output, logger.DEBUG))
	a, b := net.Pipe()
	c.dial = func(string, string) (net.Conn, error) { return a, nil }
	go func() {
		defer b.Close()
		broker(b)
	}()
	t.Cleanup(func() { c.Disconnect() })
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	return c
}

// These tests retain the real 20s ping interval and 10s response timeout.
// net.Pipe has no TCP buffers: drain client writes independently of sending
// responses, so the fake broker does not manufacture a full-duplex deadlock.
func TestNatiuPingResponseAfterRepeatedIdle(t *testing.T) {
	for _, load := range []bool{false, true} {
		name := "idle"
		if load {
			name = "concurrent_publish"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			responses := make(chan time.Time, 2)
			brokerErr := make(chan error, 1)
			published := make(chan int, 1)
			var logs pingLog
			c := pingRegressionClient(t, &logs, func(b net.Conn) {
				b.SetDeadline(time.Now().Add(55 * time.Second))
				r := bufio.NewReader(b)
				if _, err := readPacket(r); err != nil {
					brokerErr <- err
					return
				}
				if _, err := b.Write([]byte{0x20, 2, 0, 0}); err != nil {
					brokerErr <- err
					return
				}
				pings := make(chan struct{}, 4)
				drained := make(chan struct{})
				go func() {
					defer close(drained)
					count := 0
					defer func() { published <- count }()
					for {
						p, err := readPacket(r)
						if err != nil {
							return
						}
						switch p.header >> 4 {
						case 3:
							n := 0
							if len(p.data) >= 2 {
								n = int(p.data[0])<<8 | int(p.data[1])
							}
							if p.header != 0x30 || n != 1 || len(p.data) != 4 || p.data[2] != 'x' || p.data[3] != 'y' {
								brokerErr <- fmt.Errorf("corrupted/interleaved PUBLISH: %#v", p)
								return
							}
							count++
						case 12:
							if p.header != 0xc0 || len(p.data) != 0 {
								brokerErr <- fmt.Errorf("bad PINGREQ: %#v", p)
								return
							}
							pings <- struct{}{}
						case 14:
							return
						default:
							brokerErr <- fmt.Errorf("unexpected packet: %#v", p)
							return
						}
					}
				}()
				for i := 0; i < 2; i++ {
					select {
					case <-pings:
					case <-drained:
						return
					}
					// Repeated idle polls precede the ping. Split its reply
					// across the next idle deadline; completion must refresh it.
					time.Sleep(1900 * time.Millisecond)
					if _, err := b.Write([]byte{0xd0}); err != nil {
						brokerErr <- err
						return
					}
					time.Sleep(200 * time.Millisecond)
					if _, err := b.Write([]byte{0}); err != nil {
						brokerErr <- err
						return
					}
					responses <- time.Now()
				}
				<-drained
			})
			workDone := make(chan error, 1)
			if load {
				go func() {
					time.Sleep(18 * time.Second)
					var wg sync.WaitGroup
					errs := make(chan error, 4)
					for worker := 0; worker < 4; worker++ {
						wg.Add(1)
						go func() {
							defer wg.Done()
							for i := 0; i < 80; i++ {
								if err := c.Publish("x", []byte("y"), 0, false, 0); err != nil {
									errs <- err
									return
								}
								time.Sleep(125 * time.Millisecond)
							}
						}()
					}
					wg.Wait()
					close(errs)
					workDone <- <-errs
				}()
			}
			for i := 0; i < 2; i++ {
				select {
				case <-responses:
				case err := <-brokerErr:
					t.Fatal(err)
				case <-c.done:
					t.Fatal("receive loop stopped with valid fragmented PINGRESP")
				case <-time.After(26 * time.Second):
					t.Fatal("no automatic ping response observed")
				}
				// b.Write returning means bytes were read, not that the
				// decoder's state callback has run; allow its completion.
				native, _ := c.snapshot()
				until := time.Now().Add(time.Second)
				for native.AwaitingPingresp() && time.Now().Before(until) {
					time.Sleep(time.Millisecond)
				}
				if !c.IsConnected() || native.AwaitingPingresp() {
					t.Fatal("PINGRESP did not clear pending state")
				}
			}
			if load {
				if err := <-workDone; err != nil {
					t.Fatal(err)
				}
			}
			if err := c.Disconnect(); err != nil {
				t.Fatal(err)
			}
			want := 0
			if load {
				want = 320
			}
			if got := <-published; got != want {
				t.Fatalf("broker received %d publications, want %d", got, want)
			}
			if text := logs.text(); strings.Count(text, "PINGRESP decoded") != 2 || strings.Contains(text, "ping timeout") {
				t.Fatalf("unexpected ping diagnostics: %s", text)
			}
			t.Logf("2 fragmented PINGRESP decoded, %d valid publications received", want)
		})
	}
}

func TestNatiuPingMissingResponseTimesOut(t *testing.T) {
	for _, traffic := range []bool{false, true} {
		name := "idle"
		if traffic {
			name = "incoming_publish"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ping := make(chan time.Time, 1)
			var logs pingLog
			c := pingRegressionClient(t, &logs, func(b net.Conn) {
				b.SetDeadline(time.Now().Add(40 * time.Second))
				r := bufio.NewReader(b)
				if _, err := readPacket(r); err != nil {
					return
				}
				if _, err := b.Write([]byte{0x20, 2, 0, 0}); err != nil {
					return
				}
				drained := make(chan struct{})
				go func() {
					defer close(drained)
					for {
						p, err := readPacket(r)
						if err != nil || p.header>>4 == 14 {
							return
						}
						if p.header>>4 == 12 {
							ping <- time.Now()
						}
					}
				}()
				if !traffic {
					<-drained
					return
				}
				for {
					select {
					case <-drained:
						return
					case <-time.After(100 * time.Millisecond):
						// Traffic is not a substitute for PINGRESP.
						if _, err := b.Write([]byte{0x30, 4, 0, 1, 'x', 'y'}); err != nil {
							return
						}
					}
				}
			})
			var sent time.Time
			select {
			case sent = <-ping:
			case <-time.After(24 * time.Second):
				t.Fatal("automatic PINGREQ not sent")
			}
			select {
			case <-c.done:
				elapsed := time.Since(sent)
				if elapsed < 10*time.Second || elapsed > 13*time.Second {
					t.Fatalf("missing response stopped client after %v, want 10-13s", elapsed)
				}
				t.Logf("missing PINGRESP stopped client after %v", elapsed)
			case <-time.After(14 * time.Second):
				t.Fatal("missing PINGRESP did not stop receive loop")
			}
			native, _ := c.snapshot()
			if c.IsConnected() || native.Err() == nil || errors.Is(native.Err(), io.EOF) {
				t.Fatalf("unexpected timeout state: %v", native.Err())
			}
			text := logs.text()
			if !strings.Contains(text, "MQTT ping timeout: wait_ms=") || !strings.Contains(text, "rx_bytes=") || strings.Contains(text, "PINGRESP decoded") {
				t.Fatalf("missing timeout diagnostics: %s", text)
			}
			if traffic {
				if strings.Contains(text, "rx_bytes=0 ") || !strings.Contains(text, "last_type=3") {
					t.Fatalf("incoming traffic absent from diagnostics: %s", text)
				}
			} else if !strings.Contains(text, "rx_bytes=0 rx_packets=0 last_type=0") {
				t.Fatalf("idle timeout diagnostics changed: %s", text)
			}
			t.Log(strings.TrimSpace(text))
		})
	}
}
