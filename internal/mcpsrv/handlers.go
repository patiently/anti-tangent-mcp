package mcpsrv

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type handlers struct {
	deps Deps
}

func validateTaskSpecTool() *mcp.Tool {
	return &mcp.Tool{Name: "validate_task_spec"}
}

func checkProgressTool() *mcp.Tool {
	return &mcp.Tool{Name: "check_progress"}
}

func validateCompletionTool() *mcp.Tool {
	return &mcp.Tool{Name: "validate_completion"}
}

// ValidateTaskSpecArgs holds the input arguments for validate_task_spec.
// Fields will be filled in by Task 12.
type ValidateTaskSpecArgs struct{}

// CheckProgressArgs holds the input arguments for check_progress.
// Fields will be filled in by Task 13.
type CheckProgressArgs struct{}

// ValidateCompletionArgs holds the input arguments for validate_completion.
// Fields will be filled in by Task 14.
type ValidateCompletionArgs struct{}

// ValidateTaskSpec is a stub handler; real implementation added in Task 12.
//
// The SDK's generic AddTool requires handlers with the signature:
//
//	func(ctx, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error)
//
// (three return values, not two as shown in the plan). The Out type parameter
// is set to any here since these are stubs with no structured output yet.
func (h *handlers) ValidateTaskSpec(_ context.Context, _ *mcp.CallToolRequest, _ ValidateTaskSpecArgs) (*mcp.CallToolResult, any, error) {
	return nil, nil, errors.New("not implemented (Task 12)")
}

// CheckProgress is a stub handler; real implementation added in Task 13.
func (h *handlers) CheckProgress(_ context.Context, _ *mcp.CallToolRequest, _ CheckProgressArgs) (*mcp.CallToolResult, any, error) {
	return nil, nil, errors.New("not implemented (Task 13)")
}

// ValidateCompletion is a stub handler; real implementation added in Task 14.
func (h *handlers) ValidateCompletion(_ context.Context, _ *mcp.CallToolRequest, _ ValidateCompletionArgs) (*mcp.CallToolResult, any, error) {
	return nil, nil, errors.New("not implemented (Task 14)")
}
