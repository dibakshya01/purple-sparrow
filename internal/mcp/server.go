package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
)

// protocolVersion is the MCP revision this server implements.
const protocolVersion = "2024-11-05"

// Server is a minimal MCP server over a newline-delimited JSON-RPC 2.0 stdio
// transport.
type Server struct {
	be    *backend
	tools map[string]toolDef
	order []string
}

type toolDef struct {
	name        string
	description string
	schema      map[string]any
	handle      func(ctx context.Context, args map[string]any) (string, bool, error) // text, isError, err
}

// New builds an MCP server targeting the backend at baseURL with the given API key.
func New(baseURL, apiKey string) *Server {
	s := &Server{be: newBackend(baseURL, apiKey), tools: map[string]toolDef{}}
	s.registerTools()
	return s
}

// --- JSON-RPC wire types --------------------------------------------------

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Run reads JSON-RPC messages from in and writes responses to out until EOF.
func (s *Server) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	enc := json.NewEncoder(out)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			// JSON-RPC: a parse error carries a null id.
			_ = enc.Encode(rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "parse error"}})
			continue
		}
		resp, isNotification := s.dispatch(ctx, &req)
		if isNotification {
			continue // notifications get no response
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (s *Server) dispatch(ctx context.Context, req *rpcRequest) (rpcResponse, bool) {
	base := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		base.Result = map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "purple-sparrow", "version": "0.1"},
		}
		return base, false
	case "notifications/initialized":
		return base, true // notification
	case "ping":
		base.Result = map[string]any{}
		return base, false
	case "tools/list":
		base.Result = map[string]any{"tools": s.toolList()}
		return base, false
	case "tools/call":
		base.Result, base.Error = s.callTool(ctx, req.Params)
		return base, false
	default:
		base.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
		return base, false
	}
}

func (s *Server) toolList() []map[string]any {
	out := make([]map[string]any, 0, len(s.order))
	for _, name := range s.order {
		t := s.tools[name]
		out = append(out, map[string]any{
			"name": t.name, "description": t.description, "inputSchema": t.schema,
		})
	}
	return out
}

func (s *Server) callTool(ctx context.Context, params json.RawMessage) (any, *rpcError) {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid params"}
	}
	t, ok := s.tools[p.Name]
	if !ok {
		return nil, &rpcError{Code: -32602, Message: "unknown tool: " + p.Name}
	}
	text, isErr, err := t.handle(ctx, p.Arguments)
	if err != nil {
		// Transport/connection failures are surfaced as tool errors (agent-readable).
		return toolResult("tool error: "+err.Error(), true), nil
	}
	return toolResult(text, isErr), nil
}

func toolResult(text string, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	}
}

// --- helpers for tool handlers -------------------------------------------

func argStr(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

func argInt(args map[string]any, key string, def int) int {
	if v, ok := args[key].(float64); ok {
		return int(v)
	}
	return def
}

func httpIsError(status int) bool { return status < 200 || status >= 300 }

func escapePath(seg string) string { return url.PathEscape(seg) }

func errMsg(format string, a ...any) string { return fmt.Sprintf(format, a...) }
