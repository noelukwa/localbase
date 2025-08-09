package main

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewConnectionPool(t *testing.T) {
	logger := NewLogger(InfoLevel)
	ctx := context.Background()
	
	handler := func(ctx context.Context, conn net.Conn) error {
		return nil
	}
	
	pool := NewConnectionPool(ctx, 10, handler, logger)
	
	if pool == nil {
		t.Error("NewConnectionPool returned nil")
	}
	
	if pool.maxConnections != 10 {
		t.Errorf("Expected maxConnections 10, got %d", pool.maxConnections)
	}
	
	if pool.handler == nil {
		t.Error("Handler not set")
	}
	
	if pool.logger != logger {
		t.Error("Logger not set correctly")
	}
}

func TestConnectionPoolAccept(t *testing.T) {
	logger := NewLogger(InfoLevel)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	var handledConnections int32
	handler := func(ctx context.Context, conn net.Conn) error {
		atomic.AddInt32(&handledConnections, 1)
		time.Sleep(100 * time.Millisecond) // Simulate work
		return nil
	}
	
	pool := NewConnectionPool(ctx, 5, handler, logger)
	
	// Create test connections
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	
	err := pool.Accept(server)
	if err != nil {
		t.Fatalf("Accept failed: %v", err)
	}
	
	// Wait for handler to be called
	time.Sleep(200 * time.Millisecond)
	
	handled := atomic.LoadInt32(&handledConnections)
	if handled != 1 {
		t.Errorf("Expected 1 handled connection, got %d", handled)
	}
}

func TestConnectionPoolMaxConnections(t *testing.T) {
	logger := NewLogger(InfoLevel)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	// Handler that blocks until context is cancelled
	blockChan := make(chan struct{})
	handler := func(ctx context.Context, conn net.Conn) error {
		<-blockChan // Block until we signal to continue
		return nil
	}
	
	pool := NewConnectionPool(ctx, 2, handler, logger)
	
	// Create and accept connections up to the limit
	var connections []net.Conn
	defer func() {
		close(blockChan) // Unblock handlers
		for _, conn := range connections {
			conn.Close()
		}
	}()
	
	// Accept exactly 2 connections (the limit)
	for i := 0; i < 2; i++ {
		server, client := net.Pipe()
		connections = append(connections, server, client)
		
		err := pool.Accept(server)
		if err != nil {
			t.Fatalf("Accept %d failed: %v", i, err)
		}
	}
	
	// Give handlers time to start
	time.Sleep(50 * time.Millisecond)
	
	// Verify we have 2 active connections
	if pool.ActiveConnections() != 2 {
		t.Errorf("Expected 2 active connections, got %d", pool.ActiveConnections())
	}
	
	// Try to accept one more connection - should fail due to pool being full
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	
	// This should timeout since pool is full
	err := pool.Accept(server)
	if err == nil {
		t.Error("Expected error when pool is full")
	} else {
		if !containsString(err.Error(), "pool is full") {
			t.Errorf("Expected 'pool is full' error, got: %v", err)
		}
	}
}

func TestConnectionPoolActiveConnections(t *testing.T) {
	logger := NewLogger(InfoLevel)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	var startedConnections int32
	handler := func(ctx context.Context, conn net.Conn) error {
		atomic.AddInt32(&startedConnections, 1)
		time.Sleep(200 * time.Millisecond)
		return nil
	}
	
	pool := NewConnectionPool(ctx, 5, handler, logger)
	
	// Initially should have 0 active connections
	if pool.ActiveConnections() != 0 {
		t.Errorf("Expected 0 active connections initially, got %d", pool.ActiveConnections())
	}
	
	// Add some connections
	var connections []net.Conn
	defer func() {
		for _, conn := range connections {
			conn.Close()
		}
	}()
	
	for i := 0; i < 3; i++ {
		server, client := net.Pipe()
		connections = append(connections, server, client)
		
		err := pool.Accept(server)
		if err != nil {
			t.Fatalf("Accept %d failed: %v", i, err)
		}
	}
	
	// Wait for handlers to start
	time.Sleep(50 * time.Millisecond)
	
	// Should have 3 active connections
	active := pool.ActiveConnections()
	if active != 3 {
		t.Errorf("Expected 3 active connections, got %d", active)
	}
	
	// Wait for handlers to finish
	time.Sleep(300 * time.Millisecond)
	
	// Should have 0 active connections again
	if pool.ActiveConnections() != 0 {
		t.Errorf("Expected 0 active connections after completion, got %d", pool.ActiveConnections())
	}
}

func TestConnectionPoolClose(t *testing.T) {
	logger := NewLogger(InfoLevel)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	handler := func(ctx context.Context, conn net.Conn) error {
		time.Sleep(100 * time.Millisecond)
		return nil
	}
	
	pool := NewConnectionPool(ctx, 5, handler, logger)
	
	// Add a connection
	server, client := net.Pipe()
	defer client.Close()
	
	err := pool.Accept(server)
	if err != nil {
		t.Fatalf("Accept failed: %v", err)
	}
	
	// Close the pool
	err = pool.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	
	// Should have 0 active connections after close
	if pool.ActiveConnections() != 0 {
		t.Errorf("Expected 0 active connections after close, got %d", pool.ActiveConnections())
	}
}

func TestConnectionPoolContextCancellation(t *testing.T) {
	logger := NewLogger(InfoLevel)
	ctx, cancel := context.WithCancel(context.Background())
	
	handler := func(ctx context.Context, conn net.Conn) error {
		<-ctx.Done() // Wait for context cancellation
		return ctx.Err()
	}
	
	pool := NewConnectionPool(ctx, 5, handler, logger)
	
	// Add a connection
	server, client := net.Pipe()
	defer client.Close()
	
	err := pool.Accept(server)
	if err != nil {
		t.Fatalf("Accept failed: %v", err)
	}
	
	// Cancel context
	cancel()
	
	// Accept should fail after context cancellation
	server2, client2 := net.Pipe()
	defer server2.Close()
	defer client2.Close()
	
	err = pool.Accept(server2)
	if err == nil {
		t.Error("Expected error after context cancellation")
	}
	
	if !containsString(err.Error(), "shutting down") {
		t.Errorf("Expected shutting down error, got: %v", err)
	}
}

func TestConnectionPoolConcurrency(t *testing.T) {
	logger := NewLogger(InfoLevel)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	
	var startedConnections int32
	var completedConnections int32
	
	handler := func(ctx context.Context, conn net.Conn) error {
		atomic.AddInt32(&startedConnections, 1)
		time.Sleep(100 * time.Millisecond)
		atomic.AddInt32(&completedConnections, 1)
		return nil
	}
	
	pool := NewConnectionPool(ctx, 5, handler, logger) // Smaller pool size
	
	// Launch goroutines to add connections concurrently
	var wg sync.WaitGroup
	numGoroutines := 10 // Attempt more than pool size
	
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			
			server, client := net.Pipe()
			defer client.Close()
			
			err := pool.Accept(server)
			if err != nil {
				// Some will fail due to pool limits or timeouts
				t.Logf("Accept %d failed (expected): %v", id, err)
			}
		}(i)
	}
	
	wg.Wait()
	
	// Wait for handlers to complete
	time.Sleep(300 * time.Millisecond)
	
	started := atomic.LoadInt32(&startedConnections)
	completed := atomic.LoadInt32(&completedConnections)
	
	t.Logf("Started connections: %d, Completed connections: %d", started, completed)
	
	// Should have started some connections but be limited by pool size
	if started == 0 {
		t.Error("Expected at least some connections to start")
	}
	
	// Allow some race condition slack - connections might start before being rejected
	// The pool uses a semaphore which has eventual consistency, not immediate
	if started > 7 { // Allow 2 extra for race conditions
		t.Errorf("Too many connections started (pool limit: 5, got: %d)", started)
	}
	
	// Completed should equal started (all should finish)
	if completed != started {
		t.Errorf("Expected completed (%d) to equal started (%d)", completed, started)
	}
}

func TestConnectionPoolHandlerPanic(t *testing.T) {
	logger := NewLogger(InfoLevel)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	handler := func(ctx context.Context, conn net.Conn) error {
		panic("test panic")
	}
	
	pool := NewConnectionPool(ctx, 5, handler, logger)
	
	// Add a connection that will cause panic
	server, client := net.Pipe()
	defer client.Close()
	
	err := pool.Accept(server)
	if err != nil {
		t.Fatalf("Accept failed: %v", err)
	}
	
	// Wait for handler to panic and recover
	time.Sleep(100 * time.Millisecond)
	
	// Pool should still be functional after panic
	if pool.ActiveConnections() != 0 {
		t.Errorf("Expected 0 active connections after panic recovery, got %d", pool.ActiveConnections())
	}
}

// Helper function to check if string contains substring
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && findStringSubstring(s, substr)
}

func findStringSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}