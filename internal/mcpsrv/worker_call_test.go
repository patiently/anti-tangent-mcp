package mcpsrv

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/prompts"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
)

type fakeWorkerReviewer struct {
	resp providers.Response
	err  error
	got  providers.Request
	// calls counts Review invocations. Purely additive to the fake — lets a
	// caller assert the provider was (or was not) reached at all, distinct
	// from asserting on the content of the last request via got.
	calls int
}

func (f *fakeWorkerReviewer) Name() string { return "fake" }
func (f *fakeWorkerReviewer) Review(_ context.Context, r providers.Request) (providers.Response, error) {
	f.calls++
	f.got = r
	return f.resp, f.err
}

func TestWorkerSchemaIsStrictOneField(t *testing.T) {
	var m map[string]any
	require.NoError(t, json.Unmarshal(workerSchema("answer"), &m), "schema is not valid JSON")

	assert.Equal(t, "object", m["type"])

	props, _ := m["properties"].(map[string]any)
	assert.Len(t, props, 1, "want exactly one property, got %v", props)

	prop, _ := props["answer"].(map[string]any)
	require.NotNil(t, prop, "schema missing the answer property")
	assert.Equal(t, "string", prop["type"])

	req, _ := m["required"].([]any)
	require.Len(t, req, 1, "required = %v, want exactly [answer]", req)
	assert.Equal(t, "answer", req[0])

	assert.Equal(t, false, m["additionalProperties"], "schema must set additionalProperties:false")
}

func TestRunWorkerExtractsField(t *testing.T) {
	f := &fakeWorkerReviewer{resp: providers.Response{
		RawJSON:      []byte(`{"answer":"- a.go: does X"}`),
		Model:        "anthropic:claude-haiku-4-5-20251001",
		InputTokens:  120,
		OutputTokens: 9,
	}}
	h := &handlers{deps: Deps{Reviews: providers.Registry{"anthropic": f}}}
	got, err := h.runWorker(context.Background(),
		config.ModelRef{Provider: "anthropic", Model: "claude-haiku-4-5-20251001"},
		prompts.Output{System: "sys", User: "usr"}, 4096, "answer")
	require.NoError(t, err)

	assert.Equal(t, "- a.go: does X", got.Text)
	assert.Equal(t, 120, got.InputTokens)
	assert.Equal(t, 9, got.OutputTokens)
	assert.Equal(t, 4096, f.got.MaxTokens)
	assert.Equal(t, "anthropic:claude-haiku-4-5-20251001", got.Model, "want the provider's reported model")
	assert.GreaterOrEqual(t, got.ReviewMS, int64(0))
}

func TestRunWorkerFallsBackToConfiguredModel(t *testing.T) {
	// An empty Response.Model must not produce an empty model_used.
	f := &fakeWorkerReviewer{resp: providers.Response{RawJSON: []byte(`{"answer":"x"}`)}}
	h := &handlers{deps: Deps{Reviews: providers.Registry{"anthropic": f}}}
	got, err := h.runWorker(context.Background(),
		config.ModelRef{Provider: "anthropic", Model: "claude-haiku-4-5-20251001"},
		prompts.Output{}, 10, "answer")
	require.NoError(t, err)

	assert.Equal(t, "anthropic:claude-haiku-4-5-20251001", got.Model, "want the configured ref as fallback")
}

func TestRunWorkerPropagatesTruncation(t *testing.T) {
	f := &fakeWorkerReviewer{err: providers.ErrResponseTruncated}
	h := &handlers{deps: Deps{Reviews: providers.Registry{"anthropic": f}}}
	_, err := h.runWorker(context.Background(),
		config.ModelRef{Provider: "anthropic", Model: "claude-haiku-4-5-20251001"},
		prompts.Output{}, 10, "answer")
	require.ErrorIs(t, err, providers.ErrResponseTruncated)
}

func TestRunWorkerMalformedJSONDoesNotEchoBody(t *testing.T) {
	f := &fakeWorkerReviewer{resp: providers.Response{RawJSON: []byte(`{"answer": SECRET`)}}
	h := &handlers{deps: Deps{Reviews: providers.Registry{"anthropic": f}}}
	_, err := h.runWorker(context.Background(),
		config.ModelRef{Provider: "anthropic", Model: "claude-haiku-4-5-20251001"},
		prompts.Output{}, 10, "answer")
	require.Error(t, err)

	assert.Contains(t, err.Error(), "answer", "error should name the field")
	assert.NotContains(t, err.Error(), "SECRET", "error must not echo the raw body")
}

// TestRunWorkerRejectsNullField pins the exact failure mode from finding C:
// {"answer":null} unmarshals cleanly (json's documented no-op-on-null rule
// for non-pointer targets) leaving an empty string with no error, so a
// map[string]string decode alone reports success carrying nothing. runWorker
// must fail closed instead.
func TestRunWorkerRejectsNullField(t *testing.T) {
	f := &fakeWorkerReviewer{resp: providers.Response{RawJSON: []byte(`{"answer":null}`)}}
	h := &handlers{deps: Deps{Reviews: providers.Registry{"anthropic": f}}}
	_, err := h.runWorker(context.Background(),
		config.ModelRef{Provider: "anthropic", Model: "claude-haiku-4-5-20251001"},
		prompts.Output{}, 10, "answer")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "answer", "error should name the field")
}

// TestRunWorkerRejectsEmptyField covers the plain "" case (as opposed to
// null): a provider that emits an empty string satisfies the schema and the
// map decode, but still carries nothing useful back to the caller.
func TestRunWorkerRejectsEmptyField(t *testing.T) {
	f := &fakeWorkerReviewer{resp: providers.Response{RawJSON: []byte(`{"answer":""}`)}}
	h := &handlers{deps: Deps{Reviews: providers.Registry{"anthropic": f}}}
	_, err := h.runWorker(context.Background(),
		config.ModelRef{Provider: "anthropic", Model: "claude-haiku-4-5-20251001"},
		prompts.Output{}, 10, "answer")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "answer", "error should name the field")
}

// TestRunWorkerRejectsWhitespaceOnlyField covers a value that is non-empty
// by byte length but carries no content — a naive `text == ""` check (as
// opposed to a trimmed check) would let this through.
func TestRunWorkerRejectsWhitespaceOnlyField(t *testing.T) {
	f := &fakeWorkerReviewer{resp: providers.Response{RawJSON: []byte(`{"answer":"   \n\t "}`)}}
	h := &handlers{deps: Deps{Reviews: providers.Registry{"anthropic": f}}}
	_, err := h.runWorker(context.Background(),
		config.ModelRef{Provider: "anthropic", Model: "claude-haiku-4-5-20251001"},
		prompts.Output{}, 10, "answer")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "answer", "error should name the field")
}

// TestRunWorkerRejectsUnexpectedExtraProperty covers a response that carries
// the required field AND something else. The wire schema already sets
// additionalProperties:false, but that constraint is enforced by the
// provider, not verified locally — this test pins the local, defense-in-depth
// check: a chatty/misbehaving provider that ignores its own declared schema
// and smuggles a second top-level property must not be silently accepted,
// since a map[string]string decode retains (and ignores) unknown keys with
// no error. The error must not echo the smuggled value.
func TestRunWorkerRejectsUnexpectedExtraProperty(t *testing.T) {
	f := &fakeWorkerReviewer{resp: providers.Response{RawJSON: []byte(`{"answer":"ok","commentary":"SECRET_ASIDE"}`)}}
	h := &handlers{deps: Deps{Reviews: providers.Registry{"anthropic": f}}}
	_, err := h.runWorker(context.Background(),
		config.ModelRef{Provider: "anthropic", Model: "claude-haiku-4-5-20251001"},
		prompts.Output{}, 10, "answer")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "answer", "error should name the expected field")
	assert.NotContains(t, err.Error(), "SECRET_ASIDE", "error must not echo smuggled content")
}
