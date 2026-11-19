package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func serve(t *testing.T, s *Server, lines ...string) []map[string]any {
	t.Helper()
	var out strings.Builder
	if err := s.Serve(context.Background(), strings.NewReader(strings.Join(lines, "\n")+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	var resps []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("bad response line %q: %v", l, err)
		}
		resps = append(resps, m)
	}
	return resps
}

func testServer() *Server {
	return &Server{Name: "x", Version: "1", Instructions: "hi", Tools: []Tool{{
		Name: "echo", Description: "d", InputSchema: json.RawMessage(`{"type":"object"}`),
		Call: func(_ context.Context, args map[string]any, w io.Writer) error {
			if args["fail"] == true {
				fmt.Fprint(w, "partial")
				return errors.New("boom")
			}
			if args["panic"] == true {
				panic("oops")
			}
			fmt.Fprintf(w, "got %v\n", args["v"])
			return nil
		},
	}}}
}

func TestProtocol(t *testing.T) {
	r := serve(t, testServer(),
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		``,
		`{"jsonrpc":"2.0","id":"a","method":"ping"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"nope"}`,
		`{not json`,
		`{"jsonrpc":"2.0","id":4,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`,
		`[{"jsonrpc":"2.0","id":5,"method":"ping"}]`,
	)
	if len(r) != 7 {
		t.Fatalf("want 7 responses (notification unanswered), got %d: %v", len(r), r)
	}
	init := r[0]["result"].(map[string]any)
	if init["protocolVersion"] != "2025-03-26" || init["instructions"] != "hi" {
		t.Errorf("initialize: %v", init)
	}
	if r[1]["id"] != "a" || r[1]["result"] == nil {
		t.Errorf("ping: %v", r[1])
	}
	tools := r[2]["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "echo" {
		t.Errorf("tools/list: %v", r[2])
	}
	if code := r[3]["error"].(map[string]any)["code"]; code != float64(codeNoMethod) {
		t.Errorf("unknown method: %v", r[3])
	}
	if code := r[4]["error"].(map[string]any)["code"]; code != float64(codeParse) {
		t.Errorf("parse error: %v", r[4])
	}
	if v := r[5]["result"].(map[string]any)["protocolVersion"]; v != protocolVersions[0] {
		t.Errorf("unknown version should get latest, got %v", v)
	}
	if r[6]["error"] == nil {
		t.Errorf("batch should be rejected: %v", r[6])
	}
}

func TestToolsCall(t *testing.T) {
	r := serve(t, testServer(),
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"v":7}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"fail":true}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"panic":true}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"zzz"}}`,
	)
	text := func(m map[string]any) (string, bool) {
		res := m["result"].(map[string]any)
		c := res["content"].([]any)[0].(map[string]any)
		isErr, _ := res["isError"].(bool)
		return c["text"].(string), isErr
	}
	if s, e := text(r[0]); s != "got 7" || e {
		t.Errorf("ok call: %q %v", s, e)
	}
	if s, e := text(r[1]); s != "partial\nerror: boom" || !e {
		t.Errorf("failing call: %q %v", s, e)
	}
	if s, e := text(r[2]); !strings.Contains(s, "oops") || !e {
		t.Errorf("panicking call: %q %v", s, e)
	}
	if r[3]["error"].(map[string]any)["code"] != float64(codeInvalidParams) {
		t.Errorf("unknown tool: %v", r[3])
	}
}
