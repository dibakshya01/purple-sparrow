// Package mcp implements a minimal Model Context Protocol (MCP) server over stdio
// that exposes a running Orange Crow backend to a coding agent as tools. The
// MCP process is a thin client of the backend's REST API, authenticating with an
// API key — the same architecture an external MCP server would use.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// backend is a tiny REST client for the Orange Crow API.
type backend struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func newBackend(baseURL, apiKey string) *backend {
	return &backend{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// call performs an HTTP request and returns the raw response body and status. The
// body is returned as-is so tool output is the backend's own JSON (including the
// agent error envelope on failures, which the agent can act on).
func (b *backend) call(ctx context.Context, method, path string, body any) (string, int, error) {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return "", 0, err
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, b.baseURL+path, reader)
	if err != nil {
		return "", 0, err
	}
	if b.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+b.apiKey)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := b.http.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("cannot reach backend at %s: %w", b.baseURL, err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", resp.StatusCode, err
	}
	return string(out), resp.StatusCode, nil
}
