// Package mcpsrv: the shared invocation path for the I/O-delegation tools
// (bulk_read, code_write).
//
// Why a one-field JSON schema rather than a plain-text provider branch: all
// three provider clients unconditionally unmarshal req.JSONSchema and force
// structured output, so there is no text path to use. Wrapping the answer in
// a single-field object reuses that path with a zero-line provider diff, and
// — the deciding property — makes truncation FAIL CLOSED. A response cut at
// max_tokens mid-string does not parse, so code_write refuses to write half a
// file instead of writing it. A plain-text branch would need each provider's
// own stop_reason / finish_reason / finishReason spelling to get the same
// safety. See design §3.2.
package mcpsrv

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/prompts"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
)

// workerResult is one completed worker call.
type workerResult struct {
	Text         string
	Model        string
	ReviewMS     int64
	InputTokens  int
	OutputTokens int
}

// workerSchema builds the strict single-field object schema for a worker call.
// additionalProperties:false keeps a chatty model from smuggling commentary
// alongside the payload.
func workerSchema(field string) []byte {
	return []byte(fmt.Sprintf(
		`{"type":"object","properties":{%q:{"type":"string"}},"required":[%q],"additionalProperties":false}`,
		field, field))
}

// runWorker invokes the worker model and returns the named field's value.
//
// The error from a malformed response deliberately does NOT include the raw
// body: worker input is arbitrary repository source, so echoing an unparsed
// response into an error surfaces file content through a channel that is not
// the tool's declared output.
func (h *handlers) runWorker(
	ctx context.Context,
	model config.ModelRef,
	rendered prompts.Output,
	maxTokens int,
	field string,
) (workerResult, error) {
	rv, err := h.deps.Reviews.Get(model.Provider)
	if err != nil {
		return workerResult{}, err
	}

	start := time.Now()
	resp, err := rv.Review(ctx, providers.Request{
		Model:      model.Model,
		System:     rendered.System,
		User:       rendered.User,
		MaxTokens:  maxTokens,
		JSONSchema: workerSchema(field),
	})
	ms := time.Since(start).Milliseconds()
	if err != nil {
		return workerResult{}, err
	}

	var payload map[string]string
	if err := json.Unmarshal(resp.RawJSON, &payload); err != nil {
		return workerResult{}, fmt.Errorf(
			"worker response did not parse as an object with a %q field (%d bytes)", field, len(resp.RawJSON))
	}
	text, ok := payload[field]
	if !ok {
		return workerResult{}, fmt.Errorf("worker response is missing the %q field", field)
	}

	modelUsed := resp.Model
	if modelUsed == "" {
		modelUsed = model.String()
	}
	return workerResult{
		Text:         text,
		Model:        modelUsed,
		ReviewMS:     ms,
		InputTokens:  resp.InputTokens,
		OutputTokens: resp.OutputTokens,
	}, nil
}
