// Package mcpsrv: the I/O-delegation tools. These do NOT review anything —
// they route file reading and boilerplate generation to a cheap worker model
// so the corpus never enters the implementer's context. Kept out of
// handlers.go, which is already large and is about the review loop.
package mcpsrv

import (
	"context"
	"errors"
	"fmt"
	"io"
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
// that has no session id. Unlike the reviewer-driven hooks, there is no
// severity ladder here to derive a verdict from: this result short-circuits
// before any provider call, so both Verdict and Severity are set directly.
// Severity is Critical so a caller triaging findings by severity sees this as
// blocking, consistent with how the ladder would have classified it anyway.
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

type CodeWriteArgs struct {
	Spec              string `json:"spec"           jsonschema:"required"`
	ReferencePath     string `json:"reference_path" jsonschema:"required"`
	TargetPath        string `json:"target_path,omitempty"`
	Overwrite         bool   `json:"overwrite,omitempty"`
	Model             string `json:"model,omitempty"`
	MaxTokensOverride int    `json:"max_tokens_override,omitempty"`
}

type CodeWriteResult struct {
	Written      string `json:"written,omitempty"`
	LinesWritten int    `json:"lines_written,omitempty"`
	Code         string `json:"code,omitempty"`
	ModelUsed    string `json:"model_used"`
	ReviewMS     int64  `json:"review_ms"`
	InputTokens  int    `json:"input_tokens,omitempty"`
	OutputTokens int    `json:"output_tokens,omitempty"`
}

func codeWriteTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "code_write",
		Description: "Generate pattern-following boilerplate with a cheap worker model. " +
			"reference_path is REQUIRED and absolute — the generated code matches that file's conventions. " +
			"Set target_path (absolute) to have the server write the file and return only a line count, " +
			"so the generated code never enters your context. " +
			"Generated code is VERIFIED, NOT INSPECTED: you choose the reference file and prove the result " +
			"by running the task's tests and build. You are not required to read what was generated — " +
			"reading it would put back exactly the context this tool removes. " +
			"An existing target is refused unless overwrite is true, and the parent directory must already exist. " +
			"Do NOT use this for logic requiring judgement — only for boilerplate that follows an existing pattern.",
	}
}

func (h *handlers) CodeWrite(ctx context.Context, _ *mcp.CallToolRequest, args CodeWriteArgs) (*mcp.CallToolResult, CodeWriteResult, error) {
	if strings.TrimSpace(args.Spec) == "" {
		return nil, CodeWriteResult{}, errors.New("spec is required and must not be empty")
	}
	if strings.TrimSpace(args.ReferencePath) == "" {
		return nil, CodeWriteResult{}, errors.New(
			"reference_path is required: without a file whose patterns to match, the worker generates " +
				"context-free code that fits nothing in the project")
	}

	maxTokens, _, err := effectiveMaxTokens(args.MaxTokensOverride, h.deps.Cfg.WorkerMaxTokens, h.deps.Cfg.MaxTokensCeiling)
	if err != nil {
		return nil, CodeWriteResult{}, err
	}
	model, err := h.resolveModel(args.Model, h.deps.Cfg.WorkerModel)
	if err != nil {
		return nil, CodeWriteResult{}, err
	}

	refContent, refSrc, err := resolveFileInput(args.ReferencePath, h.deps.Cfg.PlanRoots, h.deps.Cfg.MaxPayloadBytes)
	if err != nil {
		return nil, CodeWriteResult{}, fmt.Errorf("reference_path: %w", err)
	}

	rendered, err := prompts.RenderWorkerCodeWrite(prompts.WorkerCodeWriteInput{
		Spec: args.Spec, ReferencePath: refSrc.Path, ReferenceContent: refContent,
	})
	if err != nil {
		return nil, CodeWriteResult{}, fmt.Errorf("render code_write prompt: %w", err)
	}

	// The worker call happens BEFORE the target is opened, so a truncated or
	// failed generation cannot leave a zero-length file behind.
	wr, err := h.runWorker(ctx, model, rendered, maxTokens, "code")
	if errors.Is(err, providers.ErrResponseTruncated) {
		return nil, CodeWriteResult{}, fmt.Errorf(
			"worker response was cut off at the token limit and nothing was written; "+
				"raise ANTI_TANGENT_WORKER_MAX_TOKENS or narrow the spec: %w", err)
	}
	if err != nil {
		return nil, CodeWriteResult{}, err
	}

	code := stripFences(wr.Text)
	// Empty output is an error, never a zero-line write. This also keeps the
	// omitempty tags on Code / LinesWritten honest: on success both are always
	// non-zero, so neither field the tool promises can vanish from the JSON.
	if strings.TrimSpace(code) == "" {
		return nil, CodeWriteResult{}, errors.New("worker returned no code; nothing was written")
	}
	res := CodeWriteResult{
		ModelUsed:    wr.Model,
		ReviewMS:     wr.ReviewMS,
		InputTokens:  wr.InputTokens,
		OutputTokens: wr.OutputTokens,
	}
	if strings.TrimSpace(args.TargetPath) == "" {
		res.Code = code
		return nil, res, nil
	}

	f, err := openWriteTarget(args.TargetPath, h.deps.Cfg.PlanRoots, args.Overwrite)
	if err != nil {
		return nil, CodeWriteResult{}, err
	}
	written := f.Name()
	if _, err := f.WriteString(code); err != nil {
		_ = f.Close()
		return nil, CodeWriteResult{}, fmt.Errorf("write %q: %w", written, err)
	}
	// Closed explicitly, not deferred: a deferred Close discards its error, and
	// on a write path that error is where a failed flush surfaces. Reporting
	// success for a file that did not close cleanly would be a lie.
	if err := f.Close(); err != nil {
		return nil, CodeWriteResult{}, fmt.Errorf("close %q: %w", written, err)
	}
	res.Written = written
	res.LinesWritten = countLines(code)
	return nil, res, nil
}

// writeTarget is the narrow surface CodeWrite needs from an open file.
// *os.File satisfies it. It exists solely so a test can force Close to fail —
// there is no way to make a real *os.File's Close return an error on demand,
// and "a Close error is reported, not discarded" is an acceptance criterion,
// so it needs a seam.
type writeTarget interface {
	io.StringWriter
	io.Closer
	Name() string
}

// openWriteTarget is a package var for the same reason. Production always
// resolves through Task 5's resolveWriteTarget; only tests replace it.
var openWriteTarget = func(target string, roots []string, overwrite bool) (writeTarget, error) {
	return resolveWriteTarget(target, roots, overwrite)
}

// stripFences removes a markdown code fence that wraps the ENTIRE body. A
// fence appearing mid-body is left alone — it is far more likely to be a
// comment or a doc string in the generated code than a formatting artifact.
func stripFences(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") {
		return s
	}
	nl := strings.IndexByte(t, '\n')
	if nl < 0 || !strings.HasSuffix(t, "```") {
		return s
	}
	body := t[nl+1 : len(t)-len("```")]
	return strings.TrimSuffix(body, "\n") + "\n"
}

// countLines counts lines including a final line with no trailing newline.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}
