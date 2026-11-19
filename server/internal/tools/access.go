package tools

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"screenreader/internal/camera"
	"screenreader/internal/config"
)

// Access modes for the MCP tool surface (config key "access").
const (
	AccessFull     = "full"
	AccessReadOnly = "read_only"
)

// readOnlyTools is the explicit allowlist of tools available when access is
// not "full". A new tool stays hidden from read-only until added here
// (fail closed).
var readOnlyTools = map[string]bool{
	"capture":     true,
	"window_info": true,
	// list_windows is read-only information AND the app's own settings picker
	// resolves app windows through it — hiding it would break the app's UI.
	"list_windows": true,
	"list_screens": true,
	"doctor":       true,
	"wait":         true,
}

// Allow reports whether the named tool is available under the given access
// mode. An empty mode means full (absent config key). Unrecognized values
// fail closed: they get the read-only allowlist.
func Allow(name, access string) bool {
	if access == "" || access == AccessFull {
		return true
	}
	return readOnlyTools[name]
}

// accessGuard re-reads the config on every call and rejects tools the current
// access mode no longer permits. This covers long-lived stdio sessions whose
// registered tool set predates a config change (HTTP mode rebuilds the server
// per request, so the registration filter is already live there).
func accessGuard(name string) error {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return nil // unreadable config: keep the startup-time registration decision
	}
	if !Allow(name, cfg.Access) {
		return fmt.Errorf("%s is disabled: access mode is %q (toggle it from the Screen Reader menu bar item, or in its settings window)", name, cfg.Access)
	}
	return nil
}

// errOffline is the status every tool returns while the app has stopped the
// server (config key "offline"). The text IS the status the agent sees.
var errOffline = errors.New(`offline — server stopped from the Screen Reader app (start it from the menu bar item, or set "offline": false in ~/.config/screenreader/config.json)`)

// offlineGuard is the kill switch, checked before accessGuard on every call:
// while "offline" is set no tool does real work, and the refusal releases the
// camera (Close is an idempotent no-op when nothing is open) so the light
// goes off even if the caller keeps hammering tools. An unreadable config
// FAILS CLOSED — a corrupt file must never silently re-enable real clicks
// after the user pressed Stop (deliberate divergence from accessGuard's
// fail-open, which only gates which tools run).
func offlineGuard() error {
	cfg, err := config.Load(config.DefaultPath())
	switch {
	case err != nil:
		camera.Close()
		return fmt.Errorf("server refused the call: config.json is unreadable (%v) — fix or remove it, then start the server from the app", err)
	case cfg.Offline:
		camera.Close()
		return errOffline
	}
	return nil
}

// add registers the tool unless the access mode hides it, and wraps the
// handler with the per-call offline and access guards. Generic over the
// argument type so mcp.AddTool's schema inference is unchanged.
func add[A any](server *mcp.Server, access, name string, tool *mcp.Tool,
	h func(context.Context, *mcp.CallToolRequest, A) (*mcp.CallToolResult, any, error)) {
	if !Allow(name, access) {
		return
	}
	mcp.AddTool(server, tool, func(ctx context.Context, req *mcp.CallToolRequest, a A) (*mcp.CallToolResult, any, error) {
		if e := offlineGuard(); e != nil {
			return errResult(e), nil, nil
		}
		if e := accessGuard(name); e != nil {
			return errResult(e), nil, nil
		}
		return h(ctx, req, a)
	})
}
