// Package mcpsrv wires the MCP server: tool registration, request validation,
// and dispatch to the verdict pipeline.
package mcpsrv

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/session"
	"github.com/patiently/anti-tangent-mcp/internal/stats"
)

// Deps holds the dependencies required by the MCP server.
type Deps struct {
	Cfg       config.Config
	Sessions  *session.Store
	Reviews   providers.Registry
	planCache *planPassCache
	// Stats is nil when ANTI_TANGENT_STATS_DIR is unset; all call sites are
	// nil-safe no-ops in that case.
	Stats *stats.Recorder
}

// Version is the server version reported via the MCP Implementation block.
// main wires this from its own ldflags-injected version at startup.
var Version = "dev"

// New creates and returns a configured MCP server with all registered tools:
// validate_task_spec, check_progress, validate_completion, validate_plan, and
// (v0.6.0) prime_project_knowledge, extract_project_knowledge.
func New(d Deps) *mcp.Server {
	if d.planCache == nil {
		d.planCache = newPlanPassCache()
	}
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "anti-tangent-mcp",
		Version: Version,
	}, nil)

	h := &handlers{deps: d}
	mcp.AddTool(srv, validateTaskSpecTool(), h.ValidateTaskSpec)
	mcp.AddTool(srv, checkProgressTool(), h.CheckProgress)
	mcp.AddTool(srv, validateCompletionTool(), h.ValidateCompletion)
	mcp.AddTool(srv, validatePlanTool(), h.ValidatePlan)
	mcp.AddTool(srv, primeProjectKnowledgeTool(), h.PrimeProjectKnowledge)
	mcp.AddTool(srv, extractProjectKnowledgeTool(), h.ExtractProjectKnowledge)

	return srv
}
