// Package mcpsrv: the I/O-delegation tools. These do NOT review anything —
// they route file reading and boilerplate generation to a cheap worker model
// so the corpus never enters the implementer's context. Kept out of
// handlers.go, which is already large and is about the review loop.
package mcpsrv

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/patiently/anti-tangent-mcp/internal/prompts"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// maxBulkReadPaths caps one call's attachment count. 50 is upstream's limit
// and is well under what any allowlisted model's context can hold at the
// payload cap.
const maxBulkReadPaths = 50

type BulkReadArgs struct {
	Question          string   `json:"question" jsonschema:"required"`
	Paths             []string `json:"paths"    jsonschema:"required"`
	Model             string   `json:"model,omitempty"`
	MaxTokensOverride int      `json:"max_tokens_override,omitempty"`
}

// BulkReadResult is the tool response. Verdict/Findings are populated ONLY on
// a structured refusal (payload too large), matching the review tools' shape
// so a caller can branch on the same field it already knows.
type BulkReadResult struct {
	Answer       string            `json:"answer,omitempty"`
	ModelUsed    string            `json:"model_used"`
	ReviewMS     int64             `json:"review_ms"`
	InputTokens  int               `json:"input_tokens,omitempty"`
	OutputTokens int               `json:"output_tokens,omitempty"`
	FilesRead    int               `json:"files_read,omitempty"`
	BytesRead    int               `json:"bytes_read,omitempty"`
	Verdict      string            `json:"verdict,omitempty"`
	Findings     []verdict.Finding `json:"findings,omitempty"`
}

func bulkReadTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "bulk_read",
		Description: "Answer a question about one or more source files WITHOUT pulling them into your context. " +
			"The server reads the files itself and asks a cheap worker model your question, returning only the answer. " +
			"Pass ABSOLUTE paths. Ask a specific question — not 'summarise this file'. " +
			"If you intend to EDIT the code, follow the answer with a targeted Read (offset/limit) of the region it names: " +
			"the answer carries no reliable line anchors.",
	}
}

func (h *handlers) BulkRead(ctx context.Context, _ *mcp.CallToolRequest, args BulkReadArgs) (*mcp.CallToolResult, BulkReadResult, error) {
	if strings.TrimSpace(args.Question) == "" {
		return nil, BulkReadResult{}, errors.New("question is required and must not be empty")
	}
	if len(args.Paths) == 0 {
		return nil, BulkReadResult{}, errors.New("paths is required and must contain at least one absolute path")
	}
	if len(args.Paths) > maxBulkReadPaths {
		return nil, BulkReadResult{}, fmt.Errorf(
			"paths has %d entries, which exceeds the limit of %d; split the call", len(args.Paths), maxBulkReadPaths)
	}

	maxTokens, _, err := effectiveMaxTokens(args.MaxTokensOverride, h.deps.Cfg.WorkerMaxTokens, h.deps.Cfg.MaxTokensCeiling)
	if err != nil {
		return nil, BulkReadResult{}, err
	}
	model, err := h.resolveModel(args.Model, h.deps.Cfg.WorkerModel)
	if err != nil {
		return nil, BulkReadResult{}, err
	}

	// Read every file first, accumulating against the shared payload cap. The
	// cap is checked as we go so an oversized SET is refused before any
	// provider call is made, not after paying for one.
	//
	// WHAT COUNTS toward the cap: caller-controlled bytes only — the question
	// plus file contents. Template scaffolding and <file> wrappers are
	// excluded deliberately: they are server-controlled and bounded, and
	// counting them would make the effective cap drift every time the
	// template is edited. A caller's budget should track what THEY put in,
	// not how verbosely the server happens to wrap it this week.
	files := make([]prompts.WorkerFile, 0, len(args.Paths))
	total := len(args.Question)
	for _, p := range args.Paths {
		content, src, err := resolveFileInput(p, h.deps.Cfg.PlanRoots, h.deps.Cfg.MaxPayloadBytes)
		if errors.Is(err, errTooLarge) {
			return nil, bulkReadTooLarge(src.Bytes, h.deps.Cfg.MaxPayloadBytes, model.String()), nil
		}
		if err != nil {
			return nil, BulkReadResult{}, err
		}
		total += src.Bytes
		if total > h.deps.Cfg.MaxPayloadBytes {
			return nil, bulkReadTooLarge(total, h.deps.Cfg.MaxPayloadBytes, model.String()), nil
		}
		files = append(files, prompts.WorkerFile{Path: src.Path, Content: content})
	}

	rendered, err := prompts.RenderWorkerBulkRead(prompts.WorkerBulkReadInput{Question: args.Question, Files: files})
	if err != nil {
		return nil, BulkReadResult{}, fmt.Errorf("render bulk_read prompt: %w", err)
	}

	wr, err := h.runWorker(ctx, model, rendered, maxTokens, "answer")
	if errors.Is(err, providers.ErrResponseTruncated) {
		return nil, BulkReadResult{}, fmt.Errorf(
			"worker response was cut off at the token limit; raise ANTI_TANGENT_WORKER_MAX_TOKENS or ask a narrower question: %w", err)
	}
	if err != nil {
		return nil, BulkReadResult{}, err
	}

	bytesRead := 0
	for _, f := range files {
		bytesRead += len(f.Content)
	}
	return nil, BulkReadResult{
		Answer:       wr.Text,
		ModelUsed:    wr.Model,
		ReviewMS:     wr.ReviewMS,
		InputTokens:  wr.InputTokens,
		OutputTokens: wr.OutputTokens,
		FilesRead:    len(files),
		BytesRead:    bytesRead,
	}, nil
}

// bulkReadTooLarge mirrors tooLargeEnvelope's shape (handlers.go) for a tool
// that has no session id. Critical severity so the ladder derives fail from
// one critical, matching the explicit Verdict: fail.
func bulkReadTooLarge(size, limit int, model string) BulkReadResult {
	return BulkReadResult{
		ModelUsed: model,
		Verdict:   string(verdict.VerdictFail),
		Findings: []verdict.Finding{{
			Severity:   verdict.SeverityCritical,
			Category:   verdict.CategoryTooLarge,
			Criterion:  "payload",
			Evidence:   fmt.Sprintf("payload %d bytes exceeds cap %d", size, limit),
			Suggestion: "Split the call across fewer paths, or raise ANTI_TANGENT_MAX_PAYLOAD_BYTES.",
		}},
	}
}
