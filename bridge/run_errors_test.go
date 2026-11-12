package bridge

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
)

type runErrorLog struct {
	mu      sync.Mutex
	entries []string
	seen    chan struct{}
}

func (*runErrorLog) Debug(string, ...any)      {}
func (*runErrorLog) Info(string, ...any)       {}
func (*runErrorLog) Warn(string, ...any)       {}
func (*runErrorLog) SetLevel(logger.LogLevel)  {}
func (*runErrorLog) GetLevel() logger.LogLevel { return logger.ERROR }
func (l *runErrorLog) Error(format string, args ...any) {
	l.mu.Lock()
	l.entries = append(l.entries, fmt.Sprintf(format, args...))
	l.mu.Unlock()
	if l.seen != nil {
		select {
		case l.seen <- struct{}{}:
		default:
		}
	}
}
func (l *runErrorLog) messages() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.entries, "\n")
}

type runCleanupMoon struct {
	sessionMoonraker
	connectErr, closeErr error
}

func (m runCleanupMoon) Connect(ctx context.Context) error {
	if m.connectErr != nil {
		m.p.cancel()
		return m.connectErr
	}
	return m.sessionMoonraker.Connect(ctx)
}
func (m runCleanupMoon) Disconnect() error {
	if err := m.sessionMoonraker.Disconnect(); err != nil {
		return err
	}
	return m.closeErr
}

func TestRunMoonrakerCleanupErrorLogging(t *testing.T) {
	for _, startupFailure := range []bool{false, true} {
		for _, cleanupFailure := range []bool{false, true} {
			name := fmt.Sprintf("startup=%t/cleanup=%t", startupFailure, cleanupFailure)
			t.Run(name, func(t *testing.T) {
				if startupFailure && !testMCUSessionPolicy {
					t.Skip("host does not own the failed partial connect")
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				p := &sessionProbe{drop: "cancel", cancel: cancel}
				cfg := config.DefaultConfig()
				cfg.MQTT.CommandsEnabled = false
				log := &runErrorLog{}
				moon := runCleanupMoon{sessionMoonraker: sessionMoonraker{p}}
				if startupFailure {
					moon.connectErr = errors.New("offline connect failure")
				}
				if cleanupFailure {
					moon.closeErr = errors.New("offline cleanup failure")
				}
				app := New(cfg, sessionMQTT{p}, log)
				app.moonrakerClient = moon
				err := app.Run(ctx)
				if startupFailure {
					if !errors.Is(err, context.Canceled) || errors.Is(err, moon.closeErr) {
						t.Fatalf("primary startup error changed: %v", err)
					}
				} else if err != nil {
					t.Fatalf("normal context-driven shutdown changed: %v", err)
				}
				wantLog := ""
				if cleanupFailure {
					wantLog = "Failed to disconnect from Moonraker: offline cleanup failure"
				}
				if log.messages() != wantLog {
					t.Fatalf("cleanup error log=%q want=%q", log.messages(), wantLog)
				}
				p.Lock()
				defer p.Unlock()
				if !reflect.DeepEqual(p.cleanup, []string{"moonraker", "mqtt"}) {
					t.Fatalf("cleanup order/count changed: %v", p.cleanup)
				}
			})
		}
	}
}

func TestPeriodicMoonrakerErrorStyle(t *testing.T) {
	if !testMCUSessionPolicy {
		t.Skip("legacy helper's failure branch is not selected on host")
	}
	cfg := config.DefaultConfig()
	cfg.Moonraker.CallInterval = 1
	p := &sessionProbe{mqttUp: true}
	app := New(cfg, sessionMQTT{p}, &runErrorLog{})
	app.moonrakerClient = sessionMoonraker{p}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := app.periodicMonitoring(ctx)
	if err == nil || err.Error() != "moonraker disconnected; session recovery required" || app.DiagnosticStage() != 6 {
		t.Fatalf("legacy helper error=%v stage=%d", err, app.DiagnosticStage())
	}
}
