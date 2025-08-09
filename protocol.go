package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"
)

// Protocol version for compatibility checking
const ProtocolVersion = "1.0"

// Request represents a JSON-RPC request
type Request struct {
	Version string                 `json:"version"`
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params,omitempty"`
	ID      string                 `json:"id"`
}

// Response represents a JSON-RPC response
type Response struct {
	Version string      `json:"version"`
	Result  interface{} `json:"result,omitempty"`
	Error   *Error      `json:"error,omitempty"`
	ID      string      `json:"id"`
}

// Error represents a JSON-RPC error
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data,omitempty"`
}

// Error implements the error interface
func (e *Error) Error() string {
	if e.Data != "" {
		return fmt.Sprintf("%s (code: %d, data: %s)", e.Message, e.Code, e.Data)
	}
	return fmt.Sprintf("%s (code: %d)", e.Message, e.Code)
}

// Common error codes
const (
	ErrorCodeInvalidRequest = -32600
	ErrorCodeMethodNotFound = -32601
	ErrorCodeInvalidParams  = -32602
	ErrorCodeInternalError  = -32603
	ErrorCodeTimeout        = -32001
	ErrorCodeValidation     = -32002
)

// ProtocolHandler handles JSON-RPC protocol communication
type ProtocolHandler struct {
	service      DomainService
	validator    Validator
	logger       Logger
	shutdownFunc func() // Called when shutdown command is received
}

// NewProtocolHandler creates a new protocol handler
func NewProtocolHandler(service DomainService, validator Validator, logger Logger) *ProtocolHandler {
	return &ProtocolHandler{
		service:   service,
		validator: validator,
		logger:    logger,
	}
}

// NewProtocolHandlerWithShutdown creates a new protocol handler with shutdown capability
func NewProtocolHandlerWithShutdown(service DomainService, validator Validator, logger Logger, shutdownFunc func()) *ProtocolHandler {
	return &ProtocolHandler{
		service:      service,
		validator:    validator,
		logger:       logger,
		shutdownFunc: shutdownFunc,
	}
}

// HandleConnection processes a client connection
func (p *ProtocolHandler) HandleConnection(ctx context.Context, conn net.Conn) error {
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	
	// Set initial deadline for reading request
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	
	// Read request
	line, err := reader.ReadBytes('\n')
	if err != nil {
		if err == io.EOF {
			return nil // Client closed connection
		}
		return p.sendError(writer, "", ErrorCodeInvalidRequest, "failed to read request", err.Error())
	}
	
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		return p.sendError(writer, "", ErrorCodeInvalidRequest, "invalid JSON", err.Error())
	}
	
	// Validate protocol version
	if req.Version != ProtocolVersion {
		return p.sendError(writer, req.ID, ErrorCodeInvalidRequest, 
			fmt.Sprintf("unsupported protocol version: %s (expected %s)", req.Version, ProtocolVersion), "")
	}
	
	// Handle request with context
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	
	// Process the request
	result, err := p.processRequest(reqCtx, &req)
	if err != nil {
		if rpcErr, ok := err.(*Error); ok {
			return p.sendError(writer, req.ID, rpcErr.Code, rpcErr.Message, rpcErr.Data)
		}
		return p.sendError(writer, req.ID, ErrorCodeInternalError, "internal error", err.Error())
	}
	
	// Send response
	return p.sendResponse(writer, req.ID, result)
}

func (p *ProtocolHandler) processRequest(ctx context.Context, req *Request) (interface{}, error) {
	p.logger.Debug("processing request", Field{"method", req.Method}, Field{"id", req.ID})
	
	switch req.Method {
	case "add":
		return p.handleAdd(ctx, req.Params)
	case "remove":
		return p.handleRemove(ctx, req.Params)
	case "list":
		return p.handleList(ctx)
	case "ping":
		return map[string]string{"status": "ok", "version": ProtocolVersion}, nil
	case "shutdown":
		return p.handleShutdown(ctx)
	default:
		return nil, &Error{
			Code:    ErrorCodeMethodNotFound,
			Message: fmt.Sprintf("unknown method: %s", req.Method),
		}
	}
}

func (p *ProtocolHandler) handleAdd(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	domain, ok := params["domain"].(string)
	if !ok {
		return nil, &Error{Code: ErrorCodeInvalidParams, Message: "missing or invalid 'domain' parameter"}
	}
	
	portFloat, ok := params["port"].(float64)
	if !ok {
		return nil, &Error{Code: ErrorCodeInvalidParams, Message: "missing or invalid 'port' parameter"}
	}
	port := int(portFloat)
	
	// Validate inputs
	if err := p.validator.ValidateDomain(domain); err != nil {
		return nil, &Error{Code: ErrorCodeValidation, Message: "invalid domain", Data: err.Error()}
	}
	
	if err := p.validator.ValidatePort(port); err != nil {
		return nil, &Error{Code: ErrorCodeValidation, Message: "invalid port", Data: err.Error()}
	}
	
	// Add domain
	if err := p.service.Add(ctx, domain, port); err != nil {
		return nil, err
	}
	
	return map[string]interface{}{
		"domain": fmt.Sprintf("%s.local", domain),
		"port":   port,
		"status": "registered",
	}, nil
}

func (p *ProtocolHandler) handleRemove(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	domain, ok := params["domain"].(string)
	if !ok {
		return nil, &Error{Code: ErrorCodeInvalidParams, Message: "missing or invalid 'domain' parameter"}
	}
	
	if err := p.service.Remove(ctx, domain); err != nil {
		return nil, err
	}
	
	return map[string]string{"status": "removed", "domain": domain}, nil
}

func (p *ProtocolHandler) handleList(ctx context.Context) (interface{}, error) {
	domains, err := p.service.List(ctx)
	if err != nil {
		return nil, err
	}
	
	return map[string]interface{}{"domains": domains}, nil
}

func (p *ProtocolHandler) handleShutdown(ctx context.Context) (interface{}, error) {
	p.logger.Info("shutdown request received")
	
	// Trigger shutdown if function is available
	if p.shutdownFunc != nil {
		go p.shutdownFunc() // Trigger shutdown asynchronously
	}
	
	return map[string]string{"status": "shutdown initiated"}, nil
}

func (p *ProtocolHandler) sendResponse(w *bufio.Writer, id string, result interface{}) error {
	resp := Response{
		Version: ProtocolVersion,
		Result:  result,
		ID:      id,
	}
	
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	
	if _, err := w.Write(data); err != nil {
		return err
	}
	
	if _, err := w.Write([]byte("\n")); err != nil {
		return err
	}
	
	return w.Flush()
}

func (p *ProtocolHandler) sendError(w *bufio.Writer, id string, code int, message, data string) error {
	resp := Response{
		Version: ProtocolVersion,
		Error: &Error{
			Code:    code,
			Message: message,
			Data:    data,
		},
		ID: id,
	}
	
	respData, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	
	if _, err := w.Write(respData); err != nil {
		return err
	}
	
	if _, err := w.Write([]byte("\n")); err != nil {
		return err
	}
	
	return w.Flush()
}