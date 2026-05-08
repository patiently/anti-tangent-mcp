// Package mcpsrv wires the MCP server: tool registration, request validation,
// and dispatch to the verdict pipeline.
package mcpsrv

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/session"
)

// Deps holds the dependencies required by the MCP server.
type Deps struct {
	Cfg      config.Config
	Sessions *session.Store
	Reviews  providers.Registry
}

// New creates and returns a configured MCP server with all three tools
// registered.
func New(d Deps) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "anti-tangent-mcp",
		Version: "0.1.0",
	}, nil)

	h := &handlers{deps: d}
	mcp.AddTool(srv, validateTaskSpecTool(), h.ValidateTaskSpec)
	mcp.AddTool(srv, checkProgressTool(), h.CheckProgress)
	mcp.AddTool(srv, validateCompletionTool(), h.ValidateCompletion)

	return srv
}
