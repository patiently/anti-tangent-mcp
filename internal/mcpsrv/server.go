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

// Version is the server version reported via the MCP Implementation block.
// main wires this from its own ldflags-injected version at startup.
var Version = "dev"

// New creates and returns a configured MCP server with all four tools
// registered: validate_task_spec, check_progress, validate_completion, and
// validate_plan.
func New(d Deps) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "anti-tangent-mcp",
		Version: Version,
	}, nil)

	h := &handlers{deps: d}
	mcp.AddTool(srv, validateTaskSpecTool(), h.ValidateTaskSpec)
	mcp.AddTool(srv, checkProgressTool(), h.CheckProgress)
	mcp.AddTool(srv, validateCompletionTool(), h.ValidateCompletion)
	mcp.AddTool(srv, validatePlanTool(), h.ValidatePlan)

	return srv
}
