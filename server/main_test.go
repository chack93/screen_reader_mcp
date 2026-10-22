package main

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"screenreader/internal/config"
)

var fullToolNames = []string{
	"capture", "click", "doctor", "drag", "focus_window", "key",
	"list_screens", "list_windows", "move", "paste", "scroll",
	"set_target", "type", "wait", "window_info",
}

var readOnlyToolNames = []string{
	"capture", "doctor", "list_screens", "list_windows", "wait", "window_info",
}

// connectClient wires an in-memory MCP client to buildServer() and returns
// the live session (closed on test cleanup).
func connectClient(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	s1, s2 := mcp.NewInMemoryTransports()
	ss, err := buildServer().Connect(ctx, s1, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { ss.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, s2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// listToolNames returns the tool names exactly as an agent sees them.
func listToolNames(t *testing.T) []string {
	t.Helper()
	cs := connectClient(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	names := make([]string, 0, len(res.Tools))
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	sort.Strings(names)
	return names
}

func sameNames(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestToolSurface_fullAccess(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no config file → default full access
	got := listToolNames(t)
	if !sameNames(got, fullToolNames) {
		t.Fatalf("full access tool set:\n got %v\nwant %v", got, fullToolNames)
	}
}

func TestToolSurface_readOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := config.Defaults()
	cfg.Access = "read_only"
	if err := config.Save(config.DefaultPath(), cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	got := listToolNames(t)
	if !sameNames(got, readOnlyToolNames) {
		t.Fatalf("read-only tool set:\n got %v\nwant %v", got, readOnlyToolNames)
	}
}

// TestAccessGuard_blocksStaleSession: a session registered under full access
// keeps the old tool LIST, but the per-call guard rejects input tools once
// the config flips to read_only.
func TestAccessGuard_blocksStaleSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cs := connectClient(t)

	cfg := config.Defaults()
	cfg.Access = "read_only"
	if err := config.Save(config.DefaultPath(), cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	ctx := context.Background()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "wait", Arguments: map[string]any{"ms": 1}})
	if err != nil {
		t.Fatalf("wait call: %v", err)
	}
	if res.IsError {
		t.Fatalf("wait rejected after switch: %v", res)
	}

	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "click", Arguments: map[string]any{"x": 0, "y": 0, "dry_run": true}})
	if err != nil {
		t.Fatalf("click call: %v", err)
	}
	if !res.IsError {
		t.Fatal("click executed after switching to read_only")
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("unexpected content type: %T", res.Content[0])
	}
	if !strings.Contains(text.Text, "disabled") {
		t.Fatalf("unexpected error text: %q", text.Text)
	}
}
