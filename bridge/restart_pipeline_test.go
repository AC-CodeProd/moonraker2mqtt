//go:build lowmem && natiu && !tinygo

package bridge

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
	"moonraker2mqtt/mqtt"
	wire "moonraker2mqtt/websocket/wire/lowmem"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type pipelineWriter struct {
	mu    sync.Mutex
	f     *os.File
	lines chan string
}

func (w *pipelineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f != nil {
		n, err := w.f.Write(p)
		if err != nil {
			return n, err
		}
		if n != len(p) {
			return n, io.ErrShortWrite
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(string(p)), "\n") {
		select {
		case w.lines <- line:
		default:
		}
	}
	return len(p), nil
}

type fakeMoonService struct {
	mu          sync.Mutex
	listener    net.Listener
	server      *http.Server
	conn        *wire.Conn
	address     string
	generation  int
	connections map[net.Conn]struct{}
	handlers    sync.WaitGroup
	serveDone   chan struct{}
	firstErr    error
}

type pipelineConnKey struct{}

func pipelinePeerShutdown(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || pipelinePeerErrno(err)
}

func pipelineClose(t *testing.T, c io.Closer) {
	t.Helper()
	if err := c.Close(); err != nil && !pipelinePeerShutdown(err) {
		t.Errorf("close pipeline fixture: %v", err)
	}
}

func (s *fakeMoonService) record(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.firstErr == nil {
		s.firstErr = err
	}
}

func (s *fakeMoonService) finish(c net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.connections[c]; exists {
		delete(s.connections, c)
		s.handlers.Done()
	}
}

func (s *fakeMoonService) start(t *testing.T) {
	t.Helper()
	l, e := net.Listen("tcp", s.address)
	if e != nil {
		t.Fatal(e)
	}
	s.mu.Lock()
	s.listener = l
	s.address = l.Addr().String()
	s.generation++
	gen := s.generation
	s.connections = make(map[net.Conn]struct{})
	s.firstErr = nil
	s.serveDone = make(chan struct{})
	serveDone := s.serveDone
	s.mu.Unlock()
	handler := wire.Handler(func(c *wire.Conn) {
		s.mu.Lock()
		s.conn = c
		s.mu.Unlock()
		defer func() {
			if err := c.Close(); err != nil && !pipelinePeerShutdown(err) {
				s.record(err)
			}
		}()
		for {
			var m map[string]any
			if e := wire.JSON.Receive(c, &m); e != nil {
				return
			}
			var result any
			switch m["method"] {
			case "server.info":
				result = map[string]any{"klippy_state": "ready"}
			case "printer.info":
				result = map[string]any{"state": "ready"}
			case "printer.objects.query":
				result = map[string]any{"probe": map[string]any{"generation": gen}}
			default:
				return
			}
			if e := wire.JSON.Send(c, map[string]any{"jsonrpc": "2.0", "id": m["id"], "result": result}); e != nil {
				return
			}
		}
	})
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			defer s.finish(req.Context().Value(pipelineConnKey{}).(net.Conn))
			handler.ServeHTTP(w, req)
		}),
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			return context.WithValue(ctx, pipelineConnKey{}, c)
		},
		ConnState: func(c net.Conn, state http.ConnState) {
			switch state {
			case http.StateNew:
				s.mu.Lock()
				s.connections[c] = struct{}{}
				s.handlers.Add(1)
				s.mu.Unlock()
			case http.StateClosed:
				s.finish(c)
			}
		},
	}
	s.server = server
	go func() {
		defer close(serveDone)
		if err := server.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.record(err)
		}
	}()
}
func (s *fakeMoonService) stop(t *testing.T) {
	t.Helper()
	s.mu.Lock()
	server, serveDone := s.server, s.serveDone
	s.server = nil
	c := s.conn
	s.conn = nil
	l := s.listener
	s.listener = nil
	s.mu.Unlock()
	if server == nil {
		return
	}
	pipelineClose(t, server)
	if l != nil {
		pipelineClose(t, l)
	}
	select {
	case <-serveDone:
	case <-time.After(4 * time.Second):
		t.Error("HTTP fixture Serve did not join")
	}
	if c != nil {
		pipelineClose(t, c)
	}
	s.mu.Lock()
	remaining := make([]net.Conn, 0, len(s.connections))
	for conn := range s.connections {
		remaining = append(remaining, conn)
	}
	s.mu.Unlock()
	for _, conn := range remaining {
		pipelineClose(t, conn)
	}
	joined := make(chan struct{})
	go func() { s.handlers.Wait(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(4 * time.Second):
		t.Error("HTTP fixture handlers did not join")
	}
	s.mu.Lock()
	err := s.firstErr
	s.mu.Unlock()
	if err != nil {
		t.Errorf("pipeline HTTP fixture failed: %v", err)
	}
}
func writeOfflinePacket(w io.Writer, packet []byte) error {
	n, err := w.Write(packet)
	if err != nil {
		return err
	}
	if n != len(packet) {
		return io.ErrShortWrite
	}
	return nil
}

func readOfflinePacket(r *bufio.Reader) (byte, []byte, error) {
	h, e := r.ReadByte()
	if e != nil {
		return 0, nil, e
	}
	length, m := 0, 1
	for i := 0; i < 4; i++ {
		b, e := r.ReadByte()
		if e != nil {
			return 0, nil, e
		}
		length += int(b&127) * m
		if b&128 == 0 {
			if length > 8192 {
				return 0, nil, fmt.Errorf("fixture packet overflow")
			}
			p := make([]byte, length)
			_, e = io.ReadFull(r, p)
			return h, p, e
		}
		m *= 128
	}
	return 0, nil, fmt.Errorf("fixture malformed packet")
}
func TestOfflineMoonrakerRestartActualPipeline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	broker, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	pubs := make(chan string, 256)
	connects := 0
	brokerDone := make(chan struct{})
	acceptedConn := make(chan net.Conn, 1)
	defer func() {
		pipelineClose(t, broker)
		select {
		case c := <-acceptedConn:
			pipelineClose(t, c)
		case <-brokerDone:
		case <-time.After(4 * time.Second):
			t.Error("broker fixture accept did not join")
			return
		}
		select {
		case <-brokerDone:
		case <-time.After(4 * time.Second):
			t.Error("broker fixture did not join")
		}
	}()
	go func() {
		defer close(brokerDone)
		c, e := broker.Accept()
		if e != nil {
			return
		}
		acceptedConn <- c
		defer pipelineClose(t, c)
		r := bufio.NewReader(c)
		for {
			h, p, e := readOfflinePacket(r)
			if e != nil {
				return
			}
			switch h >> 4 {
			case 1:
				connects++
				if err := writeOfflinePacket(c, []byte{0x20, 2, 0, 0}); err != nil {
					if ctx.Err() == nil || !pipelinePeerShutdown(err) {
						t.Errorf("pipeline MQTT fixture CONNACK write failed: %v", err)
						cancel()
					}
					return
				}
			case 3:
				if len(p) >= 2 {
					n := int(binary.BigEndian.Uint16(p))
					if len(p) >= 2+n {
						select {
						case pubs <- string(p[2:2+n]) + "=" + string(p[2+n:]):
						case <-ctx.Done():
							return
						}
					}
				}
			case 12:
				if err := writeOfflinePacket(c, []byte{0xd0, 0}); err != nil {
					if ctx.Err() == nil || !pipelinePeerShutdown(err) {
						t.Errorf("pipeline MQTT fixture PINGRESP write failed: %v", err)
						cancel()
					}
					return
				}
			case 14:
				return
			}
		}
	}()
	s := &fakeMoonService{address: "127.0.0.1:0"}
	s.start(t)
	defer s.stop(t)
	cfg := config.DefaultConfig()
	cfg.Moonraker.Host = "127.0.0.1"
	_, port, _ := net.SplitHostPort(s.address)
	cfg.Moonraker.Port, _ = strconv.Atoi(port)
	cfg.Moonraker.CallInterval = 1
	cfg.Moonraker.AutoReconnect = true
	cfg.MQTT.Host = "127.0.0.1"
	_, port, _ = net.SplitHostPort(broker.Addr().String())
	cfg.MQTT.Port, _ = strconv.Atoi(port)
	cfg.MQTT.QoS = 0
	cfg.MQTT.CommandsEnabled = false
	cfg.MQTT.TopicPrefix = "offline-recovery"
	w := &pipelineWriter{lines: make(chan string, 512)}
	if p := os.Getenv("PIPELINE_LOG"); p != "" {
		w.f, e = os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			t.Fatal(e)
		}
		defer func() {
			if err := w.f.Close(); err != nil {
				t.Errorf("close pipeline capture: %v", err)
			}
		}()
	}
	log := logger.NewSerial(w, logger.INFO)
	defer func() {
		if err := log.Err(); err != nil {
			t.Errorf("write pipeline capture: %v", err)
		}
	}()
	client := mqtt.NewNatiuClient(cfg.MQTT, log)
	app := New(cfg, client, log)
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(4 * time.Second):
			t.Error("runner cleanup did not finish")
		}
	}()
	awaitPub := func(contains string) {
		t.Helper()
		for {
			select {
			case p := <-pubs:
				if strings.Contains(p, contains) {
					t.Log("BROKER_RECEIVED", p)
					return
				}
			case e := <-done:
				done <- e
				t.Fatalf("FIXED_REQUIREMENT_RED: runner ended before publication %q: %v", contains, e)
			case <-ctx.Done():
				t.Fatal("no actual broker publication", contains)
			}
		}
	}
	awaitPub("/printer/info=")
	for i := 0; i < 2; i++ {
		s.stop(t)
		t.Log("FAULT_NETWORK_STOP", i+1)
		select {
		case e := <-done:
			done <- e
			t.Fatalf("FIXED_REQUIREMENT_RED: runner ended during Moonraker-only outage: %v", e)
		case <-time.After(2200 * time.Millisecond):
		}
		if !client.IsConnected() {
			t.Fatal("Moonraker outage disconnected MQTT")
		}
		t.Log("WAIT_OBSERVED_MQTT_RETAINED", i+1)
		s.start(t)
		t.Log("FAULT_NETWORK_RETURN", i+1)
		awaitPub(fmt.Sprintf("\"generation\":%d", i+2))
	}
	cancel()
	select {
	case e := <-done:
		done <- e
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("cleanup hung")
	}
	select {
	case <-brokerDone:
	case <-time.After(time.Second):
		t.Fatal("MQTT worker not stopped")
	}
	if connects != 1 {
		t.Fatal("MQTT reconnected", connects)
	}
	t.Log("NEW_GREEN: two network stop/return cycles, real JSON codec, real MCU MQTT broker RX, no new App, one MQTT CONNECT")
}
