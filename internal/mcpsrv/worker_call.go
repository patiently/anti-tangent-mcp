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
	"strings"
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

	// Decoded as map[string]json.RawMessage, not map[string]string: the schema
	// asks for exactly one string field, but JSON `null` unmarshals into a
	// non-pointer string with NO error (Go's documented no-op-on-null rule),
	// which would otherwise let {"answer":null} through as a "successful"
	// empty answer. Deferring the string decode of just the named field lets
	// us tell "field is JSON null" and "field is not a string at all" apart
	// from "field is a legitimately empty string" below, and lets us detect
	// an extra top-level property before ever looking at its value.
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(resp.RawJSON, &payload); err != nil {
		return workerResult{}, fmt.Errorf(
			"worker response did not parse as an object with a %q field (%d bytes)", field, len(resp.RawJSON))
	}
	raw, ok := payload[field]
	if !ok {
		return workerResult{}, fmt.Errorf("worker response is missing the %q field", field)
	}
	// The wire schema sets additionalProperties:false, but that constraint is
	// enforced by the provider, not verified here — a provider that ignores
	// its own declared schema and smuggles a second top-level property (the
	// exact "chatty model" case workerSchema's comment names) would otherwise
	// be silently accepted, since a map decode retains unknown keys with no
	// error. Reject it locally rather than trust the provider to have done so.
	if len(payload) != 1 {
		return workerResult{}, fmt.Errorf("worker response has fields beyond %q", field)
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return workerResult{}, fmt.Errorf("worker response %q field is not a string", field)
	}
	if strings.TrimSpace(text) == "" {
		return workerResult{}, fmt.Errorf("worker response %q field is empty", field)
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
