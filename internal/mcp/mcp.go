// Package mcp is a minimal Model Context Protocol server over stdio:
// newline-delimited JSON-RPC 2.0, tools only, text results only.
// It is hand-rolled so the binary stays small and starts fast.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Tool is one MCP tool.
type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	Annotations map[string]any
	// Call runs the tool. Text written to w is the result; a non-nil error
	// marks the result isError and is appended to the text.
	Call func(ctx context.Context, args map[string]any, w io.Writer) error
}

// Server serves tools over one stdio connection.
type Server struct {
	Name         string
	Version      string
	Instructions string
	Tools        []Tool
}

// Protocol versions this server speaks, newest first.
var protocolVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

// MaxMessage bounds one JSON-RPC message.
const MaxMessage = 16 << 20

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeNoMethod       = -32601
	codeInvalidParams  = -32602
)

type text struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type callResult struct {
	Content []text `json:"content"`
	IsError bool   `json:"isError,omitempty"`
}

type toolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Annotations map[string]any  `json:"annotations,omitempty"`
}

// Serve reads requests from r and writes responses to w until r ends.
// Requests are handled one at a time, in order.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), MaxMessage)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	tools := s.toolList()
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if resp := s.handle(ctx, line, tools); resp != nil {
			if err := enc.Encode(resp); err != nil {
				return err
			}
		}
	}
	return sc.Err()
}

func (s *Server) toolList() map[string]any {
	infos := make([]toolInfo, len(s.Tools))
	for i, t := range s.Tools {
		infos[i] = toolInfo{t.Name, t.Description, t.InputSchema, t.Annotations}
	}
	return map[string]any{"tools": infos}
}

func (s *Server) handle(ctx context.Context, line []byte, tools map[string]any) *response {
	if line[0] == '[' {
		// JSON-RPC batches were removed from MCP in 2025-06-18.
		return &response{JSONRPC: "2.0", ID: json.RawMessage("null"),
			Error: &rpcError{codeInvalidRequest, "batch requests are not supported"}}
	}
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		return &response{JSONRPC: "2.0", ID: json.RawMessage("null"),
			Error: &rpcError{codeParse, "parse error: " + err.Error()}}
	}
	if len(req.ID) == 0 || string(req.ID) == "null" {
		return nil // notification: never answered
	}
	resp := &response{JSONRPC: "2.0", ID: req.ID}
	result, rerr := s.dispatch(ctx, req, tools)
	if rerr != nil {
		resp.Error = rerr
	} else {
		resp.Result = result
	}
	return resp
}

func (s *Server) dispatch(ctx context.Context, req request, tools map[string]any) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := protocolVersions[0]
		for _, v := range protocolVersions {
			if v == p.ProtocolVersion {
				version = v
			}
		}
		res := map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": s.Name, "version": s.Version},
		}
		if s.Instructions != "" {
			res["instructions"] = s.Instructions
		}
		return res, nil
	case "ping":
		return struct{}{}, nil
	case "tools/list":
		return tools, nil
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{codeInvalidParams, "invalid params: " + err.Error()}
		}
		for _, t := range s.Tools {
			if t.Name == p.Name {
				return call(ctx, t, p.Arguments), nil
			}
		}
		return nil, &rpcError{codeInvalidParams, fmt.Sprintf("unknown tool %q", p.Name)}
	}
	return nil, &rpcError{codeNoMethod, "method not found: " + req.Method}
}

func call(ctx context.Context, t Tool, args map[string]any) (res callResult) {
	var out strings.Builder
	defer func() {
		if r := recover(); r != nil {
			res = callResult{Content: []text{{"text", fmt.Sprintf("internal error: %v", r)}}, IsError: true}
		}
	}()
	err := t.Call(ctx, args, &out)
	msg := out.String()
	if err != nil {
		if msg != "" && !strings.HasSuffix(msg, "\n") {
			msg += "\n"
		}
		msg += "error: " + err.Error()
	}
	return callResult{Content: []text{{"text", strings.TrimRight(msg, "\n")}}, IsError: err != nil}
}
