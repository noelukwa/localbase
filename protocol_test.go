package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// Mock implementations for testing
type mockDomainService struct {
	domains map[string]int
	addErr  error
	remErr  error
	listErr error
}

func (m *mockDomainService) Add(ctx context.Context, domain string, port int) error {
	if m.addErr != nil {
		return m.addErr
	}
	if m.domains == nil {
		m.domains = make(map[string]int)
	}
	m.domains[domain] = port
	return nil
}

func (m *mockDomainService) Remove(ctx context.Context, domain string) error {
	if m.remErr != nil {
		return m.remErr
	}
	if m.domains != nil {
		delete(m.domains, domain)
	}
	return nil
}

func (m *mockDomainService) List(ctx context.Context) ([]string, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	var domains []string
	for domain := range m.domains {
		domains = append(domains, domain)
	}
	return domains, nil
}

func (m *mockDomainService) Shutdown(ctx context.Context) error {
	return nil
}

type mockValidator struct {
	domainErr error
	portErr   error
}

func (m *mockValidator) ValidateDomain(domain string) error {
	return m.domainErr
}

func (m *mockValidator) ValidatePort(port int) error {
	return m.portErr
}

func TestNewProtocolHandler(t *testing.T) {
	service := &mockDomainService{}
	validator := &mockValidator{}
	logger := NewLogger(InfoLevel)
	
	handler := NewProtocolHandler(service, validator, logger)
	
	if handler == nil {
		t.Error("NewProtocolHandler returned nil")
	}
	if handler.service != service {
		t.Error("service not set correctly")
	}
	if handler.validator != validator {
		t.Error("validator not set correctly")
	}
	if handler.logger != logger {
		t.Error("logger not set correctly")
	}
}

func TestErrorImplementsError(t *testing.T) {
	err := &Error{
		Code:    ErrorCodeInvalidRequest,
		Message: "test error",
		Data:    "test data",
	}
	
	// Test that Error implements error interface
	var _ error = err
	
	errStr := err.Error()
	if !strings.Contains(errStr, "test error") {
		t.Errorf("Error string should contain message, got: %s", errStr)
	}
	if !strings.Contains(errStr, "test data") {
		t.Errorf("Error string should contain data, got: %s", errStr)
	}
}

func TestErrorWithoutData(t *testing.T) {
	err := &Error{
		Code:    ErrorCodeMethodNotFound,
		Message: "method not found",
	}
	
	errStr := err.Error()
	if !strings.Contains(errStr, "method not found") {
		t.Errorf("Error string should contain message, got: %s", errStr)
	}
	if !strings.Contains(errStr, "code: -32601") {
		t.Errorf("Error string should contain code, got: %s", errStr)
	}
}

func createTestConnection() (net.Conn, net.Conn) {
	server, client := net.Pipe()
	return server, client
}

// handleConnectionAsync runs HandleConnection in a goroutine to avoid deadlocks
func handleConnectionAsync(t *testing.T, handler *ProtocolHandler, ctx context.Context, server net.Conn) chan error {
	errChan := make(chan error, 1)
	go func() {
		errChan <- handler.HandleConnection(ctx, server)
	}()
	return errChan
}

// waitForHandler waits for the handler to complete and checks for errors
func waitForHandler(t *testing.T, errChan chan error, ctx context.Context) {
	select {
	case err := <-errChan:
		if err != nil {
			t.Fatalf("HandleConnection failed: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("Test timed out waiting for handler")
	}
}

// TestConn is a simple in-memory connection for testing
type TestConn struct {
	readBuf  *bytes.Buffer
	writeBuf *bytes.Buffer
	closed   bool
}

func NewTestConn() *TestConn {
	return &TestConn{
		readBuf:  &bytes.Buffer{},
		writeBuf: &bytes.Buffer{},
	}
}

func (tc *TestConn) Read(b []byte) (n int, err error) {
	if tc.closed {
		return 0, io.EOF
	}
	return tc.readBuf.Read(b)
}

func (tc *TestConn) Write(b []byte) (n int, err error) {
	if tc.closed {
		return 0, io.ErrClosedPipe
	}
	return tc.writeBuf.Write(b)
}

func (tc *TestConn) Close() error {
	tc.closed = true
	return nil
}

func (tc *TestConn) LocalAddr() net.Addr  { return nil }
func (tc *TestConn) RemoteAddr() net.Addr { return nil }
func (tc *TestConn) SetDeadline(t time.Time) error { return nil }
func (tc *TestConn) SetReadDeadline(t time.Time) error { return nil }
func (tc *TestConn) SetWriteDeadline(t time.Time) error { return nil }

func (tc *TestConn) WriteRequest(req Request) error {
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tc.readBuf.Write(data)
	return nil
}

func (tc *TestConn) ReadResponse() (Response, error) {
	var resp Response
	decoder := json.NewDecoder(tc.writeBuf)
	err := decoder.Decode(&resp)
	return resp, err
}

func TestProtocolHandlerPing(t *testing.T) {
	service := &mockDomainService{}
	validator := &mockValidator{}
	logger := NewLogger(InfoLevel)
	handler := NewProtocolHandler(service, validator, logger)
	
	conn := NewTestConn()
	defer conn.Close()
	
	// Send ping request
	req := Request{
		Version: ProtocolVersion,
		Method:  "ping",
		ID:      "test1",
	}
	
	err := conn.WriteRequest(req)
	if err != nil {
		t.Fatalf("Failed to write request: %v", err)
	}
	
	// Handle the connection
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	
	err = handler.HandleConnection(ctx, conn)
	if err != nil {
		t.Fatalf("HandleConnection failed: %v", err)
	}
	
	// Read response
	resp, err := conn.ReadResponse()
	if err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	
	if resp.Error != nil {
		t.Errorf("Unexpected error in response: %v", resp.Error)
	}
	
	if resp.ID != "test1" {
		t.Errorf("Expected ID test1, got %s", resp.ID)
	}
	
	// Check result
	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("Expected result to be map, got %T", resp.Result)
	}
	
	if result["status"] != "ok" {
		t.Errorf("Expected status ok, got %v", result["status"])
	}
}

func TestProtocolHandlerAdd(t *testing.T) {
	service := &mockDomainService{}
	validator := &mockValidator{}
	logger := NewLogger(InfoLevel)
	handler := NewProtocolHandler(service, validator, logger)
	
	server, client := createTestConnection()
	defer server.Close()
	defer client.Close()
	
	// Send add request
	req := Request{
		Version: ProtocolVersion,
		Method:  "add",
		Params: map[string]interface{}{
			"domain": "test",
			"port":   float64(3000), // JSON numbers are float64
		},
		ID: "test2",
	}
	
	// Handle the connection in a goroutine to avoid deadlock
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	
	errChan := make(chan error, 1)
	go func() {
		encoder := json.NewEncoder(client)
		encoder.Encode(req)
	}()
	
	go func() {
		errChan <- handler.HandleConnection(ctx, server)
	}()
	
	// Read response
	var resp Response
	decoder := json.NewDecoder(client)
	err := decoder.Decode(&resp)
	if err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	
	// Wait for handler to complete
	select {
	case handlerErr := <-errChan:
		if handlerErr != nil {
			t.Fatalf("HandleConnection failed: %v", handlerErr)
		}
	case <-ctx.Done():
		t.Fatalf("Test timed out")
	}
	
	if resp.Error != nil {
		t.Errorf("Unexpected error in response: %v", resp.Error)
	}
	
	// Verify domain was added
	if service.domains["test"] != 3000 {
		t.Errorf("Expected domain test with port 3000, got %v", service.domains)
	}
}

func TestProtocolHandlerInvalidMethod(t *testing.T) {
	service := &mockDomainService{}
	validator := &mockValidator{}
	logger := NewLogger(InfoLevel)
	handler := NewProtocolHandler(service, validator, logger)
	
	server, client := createTestConnection()
	defer server.Close()
	defer client.Close()
	
	// Send request with invalid method
	req := Request{
		Version: ProtocolVersion,
		Method:  "invalid_method",
		ID:      "test3",
	}
	
	// Handle the connection
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	
	errChan := handleConnectionAsync(t, handler, ctx, server)
	
	go func() {
		encoder := json.NewEncoder(client)
		encoder.Encode(req)
	}()
	
	// Read response
	var resp Response
	decoder := json.NewDecoder(client)
	err := decoder.Decode(&resp)
	if err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	
	// Wait for handler to complete
	waitForHandler(t, errChan, ctx)
	
	if resp.Error == nil {
		t.Error("Expected error for invalid method")
	}
	
	if resp.Error.Code != ErrorCodeMethodNotFound {
		t.Errorf("Expected error code %d, got %d", ErrorCodeMethodNotFound, resp.Error.Code)
	}
}

func TestProtocolHandlerInvalidJSON(t *testing.T) {
	service := &mockDomainService{}
	validator := &mockValidator{}
	logger := NewLogger(InfoLevel)
	handler := NewProtocolHandler(service, validator, logger)
	
	server, client := createTestConnection()
	defer server.Close()
	defer client.Close()
	
	// Handle the connection
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	
	errChan := handleConnectionAsync(t, handler, ctx, server)
	
	// Send invalid JSON
	go func() {
		client.Write([]byte("invalid json\n"))
	}()
	
	// Read response
	var resp Response
	decoder := json.NewDecoder(client)
	err := decoder.Decode(&resp)
	if err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	
	// Wait for handler to complete
	waitForHandler(t, errChan, ctx)
	
	if resp.Error == nil {
		t.Error("Expected error for invalid JSON")
	}
	
	if resp.Error.Code != ErrorCodeInvalidRequest {
		t.Errorf("Expected error code %d, got %d", ErrorCodeInvalidRequest, resp.Error.Code)
	}
}

func TestProtocolHandlerVersionMismatch(t *testing.T) {
	service := &mockDomainService{}
	validator := &mockValidator{}
	logger := NewLogger(InfoLevel)
	handler := NewProtocolHandler(service, validator, logger)
	
	server, client := createTestConnection()
	defer server.Close()
	defer client.Close()
	
	// Send request with wrong version
	req := Request{
		Version: "0.1",
		Method:  "ping",
		ID:      "test4",
	}
	
	// Handle the connection
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	
	errChan := handleConnectionAsync(t, handler, ctx, server)
	
	go func() {
		encoder := json.NewEncoder(client)
		encoder.Encode(req)
	}()
	
	// Read response
	var resp Response
	decoder := json.NewDecoder(client)
	err := decoder.Decode(&resp)
	if err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	
	// Wait for handler to complete
	waitForHandler(t, errChan, ctx)
	
	if resp.Error == nil {
		t.Error("Expected error for version mismatch")
	}
	
	if resp.Error.Code != ErrorCodeInvalidRequest {
		t.Errorf("Expected error code %d, got %d", ErrorCodeInvalidRequest, resp.Error.Code)
	}
}

func TestProtocolHandlerRemove(t *testing.T) {
	service := &mockDomainService{
		domains: map[string]int{"test": 3000},
	}
	validator := &mockValidator{}
	logger := NewLogger(InfoLevel)
	handler := NewProtocolHandler(service, validator, logger)
	
	server, client := createTestConnection()
	defer server.Close()
	defer client.Close()
	
	// Send remove request
	req := Request{
		Version: ProtocolVersion,
		Method:  "remove",
		Params: map[string]interface{}{
			"domain": "test",
		},
		ID: "test5",
	}
	
	// Handle the connection
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	
	errChan := handleConnectionAsync(t, handler, ctx, server)
	
	go func() {
		encoder := json.NewEncoder(client)
		encoder.Encode(req)
	}()
	
	// Read response
	var resp Response
	decoder := json.NewDecoder(client)
	err := decoder.Decode(&resp)
	if err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	
	// Wait for handler to complete
	waitForHandler(t, errChan, ctx)
	
	if resp.Error != nil {
		t.Errorf("Unexpected error in response: %v", resp.Error)
	}
	
	// Verify domain was removed
	if _, exists := service.domains["test"]; exists {
		t.Error("Expected domain to be removed")
	}
}

func TestProtocolHandlerList(t *testing.T) {
	service := &mockDomainService{
		domains: map[string]int{
			"test1": 3000,
			"test2": 4000,
		},
	}
	validator := &mockValidator{}
	logger := NewLogger(InfoLevel)
	handler := NewProtocolHandler(service, validator, logger)
	
	server, client := createTestConnection()
	defer server.Close()
	defer client.Close()
	
	// Send list request
	req := Request{
		Version: ProtocolVersion,
		Method:  "list",
		ID:      "test6",
	}
	
	// Handle the connection
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	
	errChan := handleConnectionAsync(t, handler, ctx, server)
	
	go func() {
		encoder := json.NewEncoder(client)
		encoder.Encode(req)
	}()
	
	// Read response
	var resp Response
	decoder := json.NewDecoder(client)
	err := decoder.Decode(&resp)
	if err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	
	// Wait for handler to complete
	waitForHandler(t, errChan, ctx)
	
	if resp.Error != nil {
		t.Errorf("Unexpected error in response: %v", resp.Error)
	}
	
	// Check result
	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("Expected result to be map, got %T", resp.Result)
	}
	
	domains, ok := result["domains"].([]interface{})
	if !ok {
		t.Fatalf("Expected domains to be array, got %T", result["domains"])
	}
	
	if len(domains) != 2 {
		t.Errorf("Expected 2 domains, got %d", len(domains))
	}
}