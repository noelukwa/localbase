package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
)

// LogLevel represents the severity of a log message
type LogLevel int

const (
	DebugLevel LogLevel = iota
	InfoLevel
	ErrorLevel
	FatalLevel
)

// SimpleLogger is a basic implementation of the Logger interface
type SimpleLogger struct {
	level  LogLevel
	mu     sync.Mutex
	logger *log.Logger
}

// NewLogger creates a new logger instance
func NewLogger(level LogLevel) *SimpleLogger {
	return &SimpleLogger{
		level:  level,
		logger: log.New(os.Stdout, "", log.LstdFlags),
	}
}

func (l *SimpleLogger) shouldLog(level LogLevel) bool {
	return level >= l.level
}

func (l *SimpleLogger) formatMessage(level, msg string, fields []Field) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("[%s] %s", level, msg))
	
	for _, field := range fields {
		parts = append(parts, fmt.Sprintf("%s=%v", field.Key, field.Value))
	}
	
	return strings.Join(parts, " ")
}

func (l *SimpleLogger) Debug(msg string, fields ...Field) {
	if !l.shouldLog(DebugLevel) {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logger.Println(l.formatMessage("DEBUG", msg, fields))
}

func (l *SimpleLogger) Info(msg string, fields ...Field) {
	if !l.shouldLog(InfoLevel) {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logger.Println(l.formatMessage("INFO", msg, fields))
}

func (l *SimpleLogger) Error(msg string, fields ...Field) {
	if !l.shouldLog(ErrorLevel) {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logger.Println(l.formatMessage("ERROR", msg, fields))
}

func (l *SimpleLogger) Fatal(msg string, fields ...Field) {
	l.mu.Lock()
	l.logger.Println(l.formatMessage("FATAL", msg, fields))
	l.mu.Unlock()
	os.Exit(1)
}

// ParseLogLevel converts a string to LogLevel
func ParseLogLevel(level string) LogLevel {
	switch strings.ToLower(level) {
	case "debug":
		return DebugLevel
	case "info":
		return InfoLevel
	case "error":
		return ErrorLevel
	case "fatal":
		return FatalLevel
	default:
		return InfoLevel
	}
}