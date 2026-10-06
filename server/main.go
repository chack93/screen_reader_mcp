package main

import (
	"context"
	"flag"
	"log"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"screenreader/internal/config"
	"screenreader/internal/httpmode"
	"screenreader/internal/tools"
)

const version = "0.1.0"

// buildServer constructs the MCP server with all tools registered (stdio and HTTP share it).
func buildServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "screenreader", Version: version}, nil)
	tools.RegisterReading(server)
	tools.RegisterInput(server)
	tools.RegisterTarget(server)
	return server
}

// newHTTPMux wires /health (unauthenticated) + /mcp (Streamable HTTP, JSON
// responses, bearer-gated whenever http.token is set). The token source
// re-reads config per request so edits rotate it live.
func newHTTPMux(addr string) http.Handler {
	streamable := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return buildServer() },
		&mcp.StreamableHTTPOptions{JSONResponse: true},
	)
	mux := http.NewServeMux()
	mux.Handle("/health", httpmode.HealthHandler(version, addr))
	mux.Handle("/mcp", httpmode.NewTokenAuth(func() string {
		c, _ := config.Load(config.DefaultPath())
		return c.HTTP.Token
	}, streamable))
	return mux
}

func main() {
	httpMode := flag.Bool("http", false, "serve MCP over Streamable HTTP on the configured loopback addr (config http.addr) instead of stdio")
	flag.Parse()

	if !*httpMode {
		if err := buildServer().Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			log.Fatalf("server: %v", err)
		}
		return
	}

	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	addr, err := httpmode.ValidateLoopback(cfg.HTTP.Addr)
	if err != nil {
		log.Fatalf("http: %v", err)
	}
	if cfg.HTTP.Token == "" {
		log.Printf("WARNING: http.token is empty — /mcp is unauthenticated. Set http.token in config.")
	}

	log.Printf("screenreader serving MCP over Streamable HTTP on http://%s/mcp", addr)
	if err := http.ListenAndServe(addr, newHTTPMux(addr)); err != nil {
		log.Fatalf("http: %v", err)
	}
}
