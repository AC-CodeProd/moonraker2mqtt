package logger

import (
	"fmt"
	"io"
	"sync"
)

// SerialLogger writes bounded-by-caller text messages to a serial writer.
// TinyGo's standard output is routed to the target's serial console.
type SerialLogger struct {
	mu       sync.Mutex
	level    LogLevel
	output   io.Writer
	writeErr error
}

func NewSerial(output io.Writer, level LogLevel) *SerialLogger {
	return &SerialLogger{level: level, output: output}
}
func (l *SerialLogger) write(level LogLevel, format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if level >= l.level {
		if _, err := fmt.Fprintf(l.output, "[%s] %s\n", level.String(), fmt.Sprintf(format, args...)); err != nil && l.writeErr == nil {
			l.writeErr = err
		}
	}
}

// Err returns the first output error, or nil if no write has failed. Later
// writes are still attempted, and successful writes do not clear this error.
// Reading Err does not write to the output or retry a failed message.
func (l *SerialLogger) Err() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.writeErr
}

func (l *SerialLogger) Debug(f string, a ...any) { l.write(DEBUG, f, a...) }
func (l *SerialLogger) Info(f string, a ...any)  { l.write(INFO, f, a...) }
func (l *SerialLogger) Warn(f string, a ...any)  { l.write(WARN, f, a...) }
func (l *SerialLogger) Error(f string, a ...any) { l.write(ERROR, f, a...) }
func (l *SerialLogger) SetLevel(level LogLevel)  { l.mu.Lock(); defer l.mu.Unlock(); l.level = level }
func (l *SerialLogger) GetLevel() LogLevel       { l.mu.Lock(); defer l.mu.Unlock(); return l.level }

var _ Logger = (*SerialLogger)(nil)
