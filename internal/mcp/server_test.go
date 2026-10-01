package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPInitializeListAndCall(t *testing.T) {
	// Stub backend.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/docs":
			_, _ = w.Write([]byte(`{"docs":["errors","policies"]}`))
		case "/meta":
			_, _ = w.Write([]byte(`{"engine":"sqlite","tables":[]}`))
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found"}}`))
		}
	}))
	defer backend.Close()

	s := New(backend.URL, "ps_sk_test")

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"fetch_docs","arguments":{}}}`,
	}, "\n") + "\n"

	var out bytes.Buffer
	if err := s.Run(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("run: %v", err)
	}

	var responses []map[string]any
	sc := bufio.NewScanner(&out)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("bad response json: %s", sc.Text())
		}
		responses = append(responses, m)
	}

	// The notification must NOT produce a response: 3 requests with ids -> 3 responses.
	if len(responses) != 3 {
		t.Fatalf("want 3 responses (notification suppressed), got %d: %+v", len(responses), responses)
	}

	// initialize
	initRes := responses[0]["result"].(map[string]any)
	if initRes["protocolVersion"] != protocolVersion {
		t.Fatalf("bad protocolVersion: %v", initRes["protocolVersion"])
	}

	// tools/list includes fetch_docs
	tools := responses[1]["result"].(map[string]any)["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("no tools listed")
	}
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{"fetch_docs", "get_meta", "create_table", "query_records", "create_policy"} {
		if !names[want] {
			t.Errorf("tool %q missing from tools/list", want)
		}
	}

	// tools/call fetch_docs returns the backend body as text content.
	callRes := responses[2]["result"].(map[string]any)
	content := callRes["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "policies") {
		t.Fatalf("fetch_docs content should include backend body, got %q", text)
	}
	if callRes["isError"] != false {
		t.Fatalf("fetch_docs should not be an error, got %v", callRes["isError"])
	}
}

func TestMCPUnknownMethodAndTool(t *testing.T) {
	s := New("http://127.0.0.1:1", "") // backend unused here
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"bogus/method"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"no_such_tool","arguments":{}}}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := s.Run(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(&out)
	var resps []map[string]any
	for sc.Scan() {
		var m map[string]any
		_ = json.Unmarshal(sc.Bytes(), &m)
		resps = append(resps, m)
	}
	if resps[0]["error"] == nil {
		t.Fatal("unknown method should return a JSON-RPC error")
	}
	if resps[1]["error"] == nil {
		t.Fatal("unknown tool should return a JSON-RPC error")
	}
}
