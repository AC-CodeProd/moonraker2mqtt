package logger

import (
	"fmt"
	"io"
	"log"
	"moonraker2mqtt/config"
	"moonraker2mqtt/utils"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
)

func (l LogLevel) String() string {
	switch l {
	case DEBUG:
		return "DEBUG"
	case INFO:
		return "INFO"
	case WARN:
		return "WARN"
	case ERROR:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

func ParseLogLevel(s string) LogLevel {
	switch strings.ToUpper(s) {
	case "DEBUG":
		return DEBUG
	case "INFO":
		return INFO
	case "WARN", "WARNING":
		return WARN
	case "ERROR":
		return ERROR
	default:
		return INFO
	}
}

type Logger interface {
	Debug(format string, args ...any)
	Info(format string, args ...any)
	Warn(format string, args ...any)
	Error(format string, args ...any)
	SetLevel(level LogLevel)
	GetLevel() LogLevel
}

type logger struct {
	level  LogLevel
	output io.Writer
	std    *log.Logger
	cfg    *config.LoggingConfig
}

func New(cfg *config.LoggingConfig, environment string) Logger {
	rootPath := utils.GetRootPath()
	logFile := filepath.Join(rootPath, "logs", "moonraker2mqtt.log")

	err := utils.MkdirIfNotExists(logFile)
	if err != nil {
		fmt.Printf("Failed to create logs directory: %v\n", err)
		return nil
	}
	var output io.Writer
	file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fmt.Printf("Failed to open log file: %v\n", err)
		return nil
	}

	if environment == "development" {
		output = io.MultiWriter(os.Stdout, file)
	} else {
		output = file
	}

	return &logger{
		level:  ParseLogLevel(cfg.Level),
		output: output,
		std:    log.New(output, "", 0),
		cfg:    cfg,
	}
}

func (l *logger) formatMessage(level LogLevel, format string, args ...any) string {
	timestamp := time.Now().Format("2006-01-02 15:04:05.000")
	message := fmt.Sprintf(format, args...)
	return fmt.Sprintf("[%s] %s: %s", level.String(), timestamp, message)
}

func (l *logger) log(level LogLevel, format string, args ...any) {
	if level < l.level {
		return
	}
	message := l.formatMessage(level, format, args...)
	l.std.Println(message)
}

func (l *logger) Debug(format string, args ...any) {
	l.log(DEBUG, format, args...)
}

func (l *logger) Info(format string, args ...any) {
	l.log(INFO, format, args...)
}

func (l *logger) Warn(format string, args ...any) {
	l.log(WARN, format, args...)
}

func (l *logger) Error(format string, args ...any) {
	l.log(ERROR, format, args...)
}

func (l *logger) SetLevel(level LogLevel) {
	l.level = level
}

func (l *logger) GetLevel() LogLevel {
	return l.level
}
