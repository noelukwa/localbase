package main

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// ConnectionHandler processes client connections
type ConnectionHandler func(context.Context, net.Conn) error

// ConnectionPoolImpl manages concurrent connections with rate limiting
type ConnectionPoolImpl struct {
	maxConnections int32
	activeCount    int32
	handler        ConnectionHandler
	semaphore      chan struct{}
	wg             sync.WaitGroup
	ctx            context.Context
	cancel         context.CancelFunc
	logger         Logger
}

// NewConnectionPool creates a new connection pool
func NewConnectionPool(ctx context.Context, maxConnections int, handler ConnectionHandler, logger Logger) *ConnectionPoolImpl {
	poolCtx, cancel := context.WithCancel(ctx)
	return &ConnectionPoolImpl{
		maxConnections: int32(maxConnections),
		handler:        handler,
		semaphore:      make(chan struct{}, maxConnections),
		ctx:            poolCtx,
		cancel:         cancel,
		logger:         logger,
	}
}

// Accept handles a new connection
func (p *ConnectionPoolImpl) Accept(conn net.Conn) error {
	select {
	case <-p.ctx.Done():
		conn.Close()
		return fmt.Errorf("connection pool is shutting down")
	default:
	}
	
	// Try to acquire semaphore immediately, fail if full
	select {
	case p.semaphore <- struct{}{}:
		// Successfully acquired semaphore
		atomic.AddInt32(&p.activeCount, 1)
		p.wg.Add(1)
		
		go p.handleConnection(conn)
		return nil
		
	case <-p.ctx.Done():
		// Pool is shutting down
		conn.Close()
		return fmt.Errorf("connection pool is shutting down")
		
	default:
		// Pool is full, reject immediately
		conn.Close()
		current := atomic.LoadInt32(&p.activeCount)
		return fmt.Errorf("connection pool is full (max: %d, current: %d)", p.maxConnections, current)
	}
}

func (p *ConnectionPoolImpl) handleConnection(conn net.Conn) {
	defer func() {
		conn.Close()
		<-p.semaphore // Release semaphore
		atomic.AddInt32(&p.activeCount, -1)
		p.wg.Done()
		
		if r := recover(); r != nil {
			p.logger.Error("panic in connection handler", Field{"error", r})
		}
	}()
	
	// Set reasonable timeouts
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	
	if err := p.handler(p.ctx, conn); err != nil {
		p.logger.Error("connection handler error", 
			Field{"error", err},
			Field{"remote_addr", conn.RemoteAddr().String()})
	}
}

// ActiveConnections returns the current number of active connections
func (p *ConnectionPoolImpl) ActiveConnections() int {
	return int(atomic.LoadInt32(&p.activeCount))
}

// Close gracefully shuts down the connection pool
func (p *ConnectionPoolImpl) Close() error {
	p.cancel()
	
	// Wait for all connections to finish with timeout
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	
	select {
	case <-done:
		p.logger.Info("connection pool closed gracefully")
		return nil
	case <-time.After(30 * time.Second):
		active := p.ActiveConnections()
		p.logger.Error("connection pool close timeout", Field{"active_connections", active})
		return fmt.Errorf("timeout waiting for %d connections to close", active)
	}
}