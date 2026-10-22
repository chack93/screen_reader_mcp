package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

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
		return fmt.Errorf("%s is disabled: access mode is %q (change it in the ScreenReader settings)", name, cfg.Access)
	}
	return nil
}

// add registers the tool unless the access mode hides it, and wraps the
// handler with the per-call access guard. Generic over the argument type so
// mcp.AddTool's schema inference is unchanged.
func add[A any](server *mcp.Server, access, name string, tool *mcp.Tool,
	h func(context.Context, *mcp.CallToolRequest, A) (*mcp.CallToolResult, any, error)) {
	if !Allow(name, access) {
		return
	}
	mcp.AddTool(server, tool, func(ctx context.Context, req *mcp.CallToolRequest, a A) (*mcp.CallToolResult, any, error) {
		if e := accessGuard(name); e != nil {
			return errResult(e), nil, nil
		}
		return h(ctx, req, a)
	})
}
