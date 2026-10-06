package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"screenreader/internal/config"
)

func mcpPost(t *testing.T, ts *httptest.Server, session, body string) (http.Header, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+"/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	raw, _ := io.ReadAll(resp.Body)
	if len(raw) == 0 {
		return resp.Header, map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	return resp.Header, out
}

func TestHTTPFullStack(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // clean config ⇒ no token
	ts := httptest.NewServer(newHTTPMux("127.0.0.1:18427"))
	defer ts.Close()

	// /health answers without auth
	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("health status %d", resp.StatusCode)
	}

	// MCP initialize handshake
	hdr, msg := mcpPost(t, ts, "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`)
	session := hdr.Get("Mcp-Session-Id")
	if session == "" {
		t.Fatal("no Mcp-Session-Id header in initialize response")
	}
	result, _ := msg["result"].(map[string]any)
	info, _ := result["serverInfo"].(map[string]any)
	if info["name"] != "screenreader" {
		t.Fatalf("serverInfo: %v", result)
	}

	// initialized notification, then tools/list
	mcpPost(t, ts, session, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	_, msg = mcpPost(t, ts, session, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	result, _ = msg["result"].(map[string]any)
	got, _ := result["tools"].([]any)
	if len(got) != 17 {
		var names []string
		for _, tx := range got {
			m, _ := tx.(map[string]any)
			names = append(names, m["name"].(string))
		}
		t.Fatalf("got %d tools: %v", len(got), strings.Join(names, ","))
	}
}

func TestHTTPMux_tokenEnforced(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := config.Defaults()
	cfg.HTTP.Token = "tok123"
	if err := config.Save(config.DefaultPath(), cfg); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(newHTTPMux("127.0.0.1:18427"))
	defer ts.Close()

	// no header → 401
	req, _ := http.NewRequest("POST", ts.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-header status %d, want 401", resp.StatusCode)
	}

	// correct header → initialize succeeds
	req2, _ := http.NewRequest("POST", ts.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Accept", "application/json, text/event-stream")
	req2.Header.Set("Authorization", "Bearer tok123")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		b, _ := io.ReadAll(resp2.Body)
		t.Fatalf("correct-token status %d: %s", resp2.StatusCode, b)
	}
}
