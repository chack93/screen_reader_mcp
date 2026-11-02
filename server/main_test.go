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

// TestCapture_windowOnNonAppTarget: capture picks its path by target kind (app →
// that window alone, rect/screen → the region as seen), and asking for a window
// explicitly on a target that has none fails loudly instead of quietly returning
// the region grab the caller was trying to avoid.
func TestCapture_windowOnNonAppTarget(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cs := connectClient(t)
	ctx := context.Background()

	call := func(args map[string]any) string {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "capture", Arguments: args})
		if err != nil {
			t.Fatalf("capture call: %v", err)
		}
		if !res.IsError {
			t.Fatalf("capture accepted window:true on a non-app target: %v", res.Content)
		}
		text, ok := res.Content[0].(*mcp.TextContent)
		if !ok {
			t.Fatalf("unexpected content type: %T", res.Content[0])
		}
		return text.Text
	}

	if got := call(map[string]any{"rect": map[string]any{"x": 0, "y": 0, "w": 10, "h": 10}, "window": true}); !strings.Contains(got, "needs an app target") {
		t.Fatalf("pinned rect: %q", got)
	}

	cfg := config.Defaults()
	cfg.Mode = "screen"
	if err := config.Save(config.DefaultPath(), cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	if got := call(map[string]any{"window": true}); !strings.Contains(got, "needs an app target") {
		t.Fatalf("screen mode: %q", got)
	}
}

// TestCapture_autoPathByTargetKind pins the default per target kind: an app
// target is captured as that window alone (never the region behind it), while a
// pinned rect or a whole screen is captured as it appears on screen, overlaps
// included. The chosen path is read from the source= marker rather than from
// success/failure, so the assertion holds whether or not this machine grants
// ScreenCaptureKit or Screen Recording at all.
func TestCapture_autoPathByTargetKind(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cs := connectClient(t)
	ctx := context.Background()

	text := func() string {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "capture", Arguments: map[string]any{}})
		if err != nil {
			t.Fatalf("capture call: %v", err)
		}
		var out string
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				out += tc.Text
			}
		}
		return out
	}
	save := func(mut func(*config.Config)) {
		t.Helper()
		cfg := config.Defaults()
		mut(cfg)
		if err := config.Save(config.DefaultPath(), cfg); err != nil {
			t.Fatalf("save config: %v", err)
		}
	}

	save(func(cfg *config.Config) { cfg.Mode, cfg.App = "app", "Some App" })
	if got := text(); strings.Contains(got, "source=region") {
		t.Fatalf("app target captured the region behind the window: %q", got)
	}

	save(func(cfg *config.Config) {
		cfg.Mode, cfg.App = "rect", ""
		cfg.Rect = config.Rect{X: 0, Y: 0, W: 10, H: 10}
	})
	if got := text(); strings.Contains(got, "source=window") {
		t.Fatalf("pinned rect used the window path: %q", got)
	}

	save(func(cfg *config.Config) { cfg.Mode, cfg.App, cfg.Rect = "screen", "", config.Rect{} })
	if got := text(); strings.Contains(got, "source=window") {
		t.Fatalf("screen target used the window path: %q", got)
	}
}

// TestCapture_cameraTargetWindowTrue: camera targets reject window:true
// through the generic non-app-target path, and never reach the camera.
func TestCapture_cameraTargetWindowTrue(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cs := connectClient(t)
	cfg := config.Defaults()
	cfg.Mode, cfg.Camera.ID = "camera", "does-not-exist-id"
	if err := config.Save(config.DefaultPath(), cfg); err != nil {
		t.Fatal(err)
	}
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "capture", Arguments: map[string]any{"window": true}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("camera target accepted window:true: %v", res.Content)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "needs an app target") || !strings.Contains(text, "camera") {
		t.Fatalf("text %q must name the camera kind", text)
	}
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

// TestClick_cameraModeBlocked: the target has no screen coordinates while it
// is a camera, so pointer tools fail loudly; keyboard tools keep working
// (they drive the focused app, not the target).
func TestClick_cameraModeBlocked(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cs := connectClient(t)
	cfg := config.Defaults()
	cfg.Mode = "camera"
	if err := config.Save(config.DefaultPath(), cfg); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, name := range []string{"click", "move", "scroll", "drag"} {
		// Arguments must validate against each tool's own schema (the SDK rejects
		// unknown properties before the handler runs, so a dry_run on move — which
		// has no such param — would test schema validation, not the guard).
		args := map[string]any{"x": 5, "y": 5}
		if name == "click" {
			args["dry_run"] = true
		}
		if name == "scroll" {
			args = map[string]any{"dx": 1, "dy": 1}
		}
		if name == "drag" {
			args = map[string]any{"x1": 1, "y1": 1, "x2": 2, "y2": 2, "dry_run": true}
		}
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if !res.IsError {
			t.Fatalf("%s ran in camera mode: %v", name, res.Content)
		}
		// Per-tool prefix: the guard must name the tool it blocks, so a
		// copy-pasted wrong tool name at one call site fails here.
		if text := res.Content[0].(*mcp.TextContent).Text; !strings.HasPrefix(text, name+" is disabled") {
			t.Fatalf("%s: %q", name, text)
		}
	}
	// type is NOT blocked (it needs Accessibility to run; assert only that it
	// is not refused by the camera guard) — dry path not available, so skip
	// actual execution and rely on the registration tests instead.
}

// TestWindowInfo_cameraMode: camera mode has no bounds, so window_info must
// report the device instead of erroring out of resolveRect.
func TestWindowInfo_cameraMode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cs := connectClient(t)
	cfg := config.Defaults()
	cfg.Mode, cfg.Camera.Name = "camera", "Cam X"
	if err := config.Save(config.DefaultPath(), cfg); err != nil {
		t.Fatal(err)
	}
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "window_info", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("window_info errored in camera mode: %v", res.Content)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, `mode=camera`) || !strings.Contains(text, `camera="Cam X"`) {
		t.Fatalf("text %q must report the camera", text)
	}
}
