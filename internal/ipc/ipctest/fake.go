// Package ipctest runs a fake node IPC endpoint on a unix socket for tests.
package ipctest

import (
	"bufio"
	"encoding/json"
	"net"
	"sync"
)

// Handler answers one method. Return an error with code and message to send a
// JSON-RPC error instead of a result.
type Handler func(params json.RawMessage) (any, *Error)

// Error is a JSON-RPC error the fake server sends back.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Server is a fake node listening on a unix socket.
type Server struct {
	Path string

	ln       net.Listener
	mu       sync.Mutex
	handlers map[string]Handler
	conns    map[net.Conn]struct{}
	calls    []string
	wg       sync.WaitGroup
}

// Start listens on path and answers with handlers. Methods without a handler
// get the JSON-RPC "method not found" error.
func Start(path string, handlers map[string]Handler) (*Server, error) {
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	s := &Server{Path: path, ln: ln, handlers: handlers, conns: map[net.Conn]struct{}{}}
	s.wg.Add(1)
	go s.accept()
	return s, nil
}

// Calls lists the methods received so far, in order.
func (s *Server) Calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

// Close stops listening, drops every open connection and removes the socket,
// as a node does when it shuts down.
func (s *Server) Close() {
	_ = s.ln.Close()
	s.mu.Lock()
	for c := range s.conns {
		_ = c.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *Server) accept() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns[conn] = struct{}{}
		s.mu.Unlock()
		s.wg.Add(1)
		go s.serve(conn)
	}
}

type request struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

func (s *Server) serve(conn net.Conn) {
	defer s.wg.Done()
	defer func() {
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		_ = conn.Close()
	}()
	dec := json.NewDecoder(bufio.NewReader(conn))
	enc := json.NewEncoder(conn)
	for {
		var req request
		if err := dec.Decode(&req); err != nil {
			return
		}
		s.mu.Lock()
		s.calls = append(s.calls, req.Method)
		h := s.handlers[req.Method]
		s.mu.Unlock()

		resp := response{JSONRPC: "2.0", ID: req.ID}
		if h == nil {
			resp.Error = &Error{Code: -32601, Message: "the method " + req.Method + " does not exist/is not available"}
		} else {
			result, rpcErr := h(req.Params)
			if rpcErr != nil {
				resp.Error = rpcErr
			} else if result == nil {
				resp.Result = json.RawMessage("null")
			} else {
				resp.Result = result
			}
		}
		if err := enc.Encode(resp); err != nil {
			return
		}
	}
}

// Value is a handler that always returns v.
func Value(v any) Handler {
	return func(json.RawMessage) (any, *Error) { return v, nil }
}
