package main

import (
	"context"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"screenreader/internal/tools"
)

func main() {
	server := mcp.NewServer(&mcp.Implementation{Name: "screenreader", Version: "0.1.0"}, nil)
	tools.RegisterReading(server)
	tools.RegisterInput(server)
	tools.RegisterTarget(server)
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("server: %v", err)
	}
}
