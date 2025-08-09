package main

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestNewLogger(t *testing.T) {
	logger := NewLogger(InfoLevel)
	if logger == nil {
		t.Error("NewLogger returned nil")
	}
	
	if logger.level != InfoLevel {
		t.Errorf("expected log level %d, got %d", InfoLevel, logger.level)
	}
}

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected LogLevel
	}{
		{"debug", DebugLevel},
		{"DEBUG", DebugLevel},
		{"info", InfoLevel},
		{"INFO", InfoLevel},
		{"error", ErrorLevel},
		{"ERROR", ErrorLevel},
		{"fatal", FatalLevel},
		{"FATAL", FatalLevel},
		{"unknown", InfoLevel}, // default
		{"", InfoLevel},        // default
	}
	
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			result := ParseLogLevel(test.input)
			if result != test.expected {
				t.Errorf("ParseLogLevel(%s): expected %d, got %d", test.input, test.expected, result)
			}
		})
	}
}

func TestLoggerShouldLog(t *testing.T) {
	logger := NewLogger(InfoLevel)
	
	// Should not log debug when level is Info
	if logger.shouldLog(DebugLevel) {
		t.Error("expected debug to be filtered out at info level")
	}
	
	// Should log info when level is Info
	if !logger.shouldLog(InfoLevel) {
		t.Error("expected info to be logged at info level")
	}
	
	// Should log error when level is Info
	if !logger.shouldLog(ErrorLevel) {
		t.Error("expected error to be logged at info level")
	}
	
	// Should log fatal when level is Info
	if !logger.shouldLog(FatalLevel) {
		t.Error("expected fatal to be logged at info level")
	}
}

func TestLoggerFormatMessage(t *testing.T) {
	logger := NewLogger(InfoLevel)
	
	// Test message without fields
	result := logger.formatMessage("INFO", "test message", nil)
	expected := "[INFO] test message"
	if result != expected {
		t.Errorf("expected '%s', got '%s'", expected, result)
	}
	
	// Test message with fields
	fields := []Field{
		{"key1", "value1"},
		{"key2", 123},
	}
	result = logger.formatMessage("ERROR", "test error", fields)
	if !strings.Contains(result, "[ERROR] test error") {
		t.Errorf("expected result to contain log level and message, got: %s", result)
	}
	if !strings.Contains(result, "key1=value1") {
		t.Errorf("expected result to contain field key1=value1, got: %s", result)
	}
	if !strings.Contains(result, "key2=123") {
		t.Errorf("expected result to contain field key2=123, got: %s", result)
	}
}

func TestLoggerDebug(t *testing.T) {
	// Capture log output
	var buf bytes.Buffer
	logger := NewLogger(DebugLevel)
	logger.logger = log.New(&buf, "", 0)
	
	logger.Debug("debug message", Field{"key", "value"})
	
	output := buf.String()
	if !strings.Contains(output, "[DEBUG] debug message") {
		t.Errorf("expected debug output to contain message, got: %s", output)
	}
	if !strings.Contains(output, "key=value") {
		t.Errorf("expected debug output to contain field, got: %s", output)
	}
}

func TestLoggerInfo(t *testing.T) {
	// Capture log output
	var buf bytes.Buffer
	logger := NewLogger(InfoLevel)
	logger.logger = log.New(&buf, "", 0)
	
	logger.Info("info message", Field{"key", "value"})
	
	output := buf.String()
	if !strings.Contains(output, "[INFO] info message") {
		t.Errorf("expected info output to contain message, got: %s", output)
	}
	if !strings.Contains(output, "key=value") {
		t.Errorf("expected info output to contain field, got: %s", output)
	}
}

func TestLoggerError(t *testing.T) {
	// Capture log output
	var buf bytes.Buffer
	logger := NewLogger(ErrorLevel)
	logger.logger = log.New(&buf, "", 0)
	
	logger.Error("error message", Field{"key", "value"})
	
	output := buf.String()
	if !strings.Contains(output, "[ERROR] error message") {
		t.Errorf("expected error output to contain message, got: %s", output)
	}
	if !strings.Contains(output, "key=value") {
		t.Errorf("expected error output to contain field, got: %s", output)
	}
}

func TestLoggerFiltering(t *testing.T) {
	// Test that lower-level messages are filtered out
	var buf bytes.Buffer
	logger := NewLogger(ErrorLevel)
	logger.logger = log.New(&buf, "", 0)
	
	// These should be filtered out
	logger.Debug("debug message")
	logger.Info("info message")
	
	output := buf.String()
	if output != "" {
		t.Errorf("expected no output for filtered messages, got: %s", output)
	}
	
	// This should not be filtered
	logger.Error("error message")
	output = buf.String()
	if !strings.Contains(output, "error message") {
		t.Errorf("expected error message in output, got: %s", output)
	}
}

func TestLoggerConcurrency(t *testing.T) {
	// Test that logger is safe for concurrent use
	var buf bytes.Buffer
	logger := NewLogger(InfoLevel)
	logger.logger = log.New(&buf, "", 0)
	
	done := make(chan bool, 10)
	
	// Start 10 goroutines logging concurrently
	for i := 0; i < 10; i++ {
		go func(id int) {
			logger.Info("concurrent message", Field{"id", id})
			done <- true
		}(i)
	}
	
	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}
	
	output := buf.String()
	// We should have 10 log messages
	messageCount := strings.Count(output, "concurrent message")
	if messageCount != 10 {
		t.Errorf("expected 10 log messages, got %d", messageCount)
	}
}

func TestField(t *testing.T) {
	field := Field{"test_key", "test_value"}
	
	if field.Key != "test_key" {
		t.Errorf("expected field key 'test_key', got '%s'", field.Key)
	}
	
	if field.Value != "test_value" {
		t.Errorf("expected field value 'test_value', got '%v'", field.Value)
	}
}