package mcpsrv

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
)

func workerDeps(t *testing.T, rv providers.Reviewer, roots []string) Deps {
	t.Helper()
	return Deps{
		Cfg: config.Config{
			WorkerModel:      config.ModelRef{Provider: "anthropic", Model: "claude-haiku-4-5-20251001"},
			WorkerMaxTokens:  4096,
			MaxTokensCeiling: 16384,
			MaxPayloadBytes:  204800,
			PlanRoots:        roots,
		},
		Reviews: providers.Registry{"anthropic": rv},
	}
}

func TestBulkReadRejectsEmptyQuestion(t *testing.T) {
	h := &handlers{deps: workerDeps(t, &fakeWorkerReviewer{}, nil)}
	_, _, err := h.BulkRead(context.Background(), nil, BulkReadArgs{Paths: []string{"/tmp/x"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "question", "want a question validation error")
}

func TestBulkReadRejectsTooManyPaths(t *testing.T) {
	paths := make([]string, 51)
	for i := range paths {
		paths[i] = "/tmp/x"
	}
	h := &handlers{deps: workerDeps(t, &fakeWorkerReviewer{}, nil)}
	_, _, err := h.BulkRead(context.Background(), nil, BulkReadArgs{Question: "q", Paths: paths})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "50", "want an error naming the 50-path limit")
}

func TestBulkReadRefusesOutsideRoots(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.go")
	require.NoError(t, os.WriteFile(outside, []byte("package x\n"), 0o644))
	h := &handlers{deps: workerDeps(t, &fakeWorkerReviewer{}, []string{dir})}
	_, _, err := h.BulkRead(context.Background(), nil, BulkReadArgs{Question: "q", Paths: []string{outside}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PLAN_ROOTS", "want a roots refusal")
}

func TestBulkReadHappyPath(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(p, []byte("package a\n"), 0o644))

	f := &fakeWorkerReviewer{resp: providers.Response{
		RawJSON: []byte(`{"answer":"- a.go: package a"}`), Model: "anthropic:claude-haiku-4-5-20251001",
		InputTokens: 42, OutputTokens: 7,
	}}
	h := &handlers{deps: workerDeps(t, f, []string{dir})}
	_, res, err := h.BulkRead(context.Background(), nil, BulkReadArgs{Question: "what package?", Paths: []string{p}})
	require.NoError(t, err)

	assert.Equal(t, "- a.go: package a", res.Answer)
	assert.Equal(t, 1, res.FilesRead)
	assert.Equal(t, len("package a\n"), res.BytesRead)
	assert.Equal(t, 42, res.InputTokens)
	assert.Equal(t, 7, res.OutputTokens)
}

func TestBulkReadRefusesControlCharPath(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a\nb.go")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Skipf("filesystem rejects the name: %v", err)
	}
	h := &handlers{deps: workerDeps(t, &fakeWorkerReviewer{}, []string{dir})}
	_, _, err := h.BulkRead(context.Background(), nil, BulkReadArgs{Question: "q", Paths: []string{p}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "U+", "want a control-character refusal naming the code point")
}

func TestBulkReadAnswerCarriesNoUnquotedSource(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	const sentinel = "SENTINEL_NOT_IN_ANSWER_9f3a"
	require.NoError(t, os.WriteFile(p, []byte("package a // "+sentinel+"\n"), 0o644))

	f := &fakeWorkerReviewer{resp: providers.Response{RawJSON: []byte(`{"answer":"- a.go: package a"}`)}}
	h := &handlers{deps: workerDeps(t, f, []string{dir})}
	_, res, err := h.BulkRead(context.Background(), nil, BulkReadArgs{Question: "q", Paths: []string{p}})
	require.NoError(t, err)

	b, err := json.Marshal(res)
	require.NoError(t, err)
	assert.NotContains(t, string(b), sentinel, "file content the worker did not quote leaked into the response")
}

func TestBulkReadCapCrossedOnLastFile(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for i := 0; i < 3; i++ {
		fp := filepath.Join(dir, fmt.Sprintf("f%d.go", i))
		require.NoError(t, os.WriteFile(fp, make([]byte, 40), 0o644))
		paths = append(paths, fp)
	}
	d := workerDeps(t, &fakeWorkerReviewer{}, []string{dir})
	d.Cfg.MaxPayloadBytes = 100 // question(1) + 40 + 40 fits; the third crosses
	h := &handlers{deps: d}
	_, res, err := h.BulkRead(context.Background(), nil, BulkReadArgs{Question: "q", Paths: paths})
	require.NoError(t, err, "want an envelope, not a transport error")
	assert.Equal(t, "fail", res.Verdict, "crossing the cap on the last file must refuse")
}

// catalogHas connects an in-memory client to a real server and reports whether
// the named tool is advertised. Mirrors the transport setup already used by
// internal/mcpsrv/integration_test.go — do not invent a second mechanism.
func catalogHas(t *testing.T, name string) bool {
	t.Helper()
	cfg, err := config.Load(func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "k"
		}
		return ""
	})
	require.NoError(t, err)
	srv := New(Deps{Cfg: cfg, Reviews: providers.Registry{"anthropic": &fakeWorkerReviewer{}}})

	st, ct := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	go func() { _ = srv.Run(ctx, st) }()

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err, "connect")
	t.Cleanup(func() { _ = cs.Close() })

	res, err := cs.ListTools(ctx, nil)
	require.NoError(t, err, "ListTools")
	for _, tool := range res.Tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

// Named so `-run BulkRead` selects it, and asserting ONLY bulk_read: code_write
// is not registered until Task 6, so a combined assertion here would leave
// `go test ./...` red for every task in between. Task 6 adds its own.
func TestBulkReadRegisteredInCatalog(t *testing.T) {
	assert.True(t, catalogHas(t, "bulk_read"), `tool "bulk_read" is not registered`)
}

func TestBulkReadRejectsEmptyPaths(t *testing.T) {
	h := &handlers{deps: workerDeps(t, &fakeWorkerReviewer{}, nil)}
	_, _, err := h.BulkRead(context.Background(), nil, BulkReadArgs{Question: "q"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "paths", "want a paths validation error")
}

func TestBulkReadTooLargePayload(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.go")
	require.NoError(t, os.WriteFile(p, make([]byte, 5000), 0o644))

	d := workerDeps(t, &fakeWorkerReviewer{}, []string{dir})
	d.Cfg.MaxPayloadBytes = 100
	h := &handlers{deps: d}
	_, res, err := h.BulkRead(context.Background(), nil, BulkReadArgs{Question: "q", Paths: []string{p}})
	require.NoError(t, err, "too-large must be an envelope, not a transport error")

	assert.Equal(t, "fail", res.Verdict)
	assert.NotEmpty(t, res.Findings)
	assert.Empty(t, res.Answer, "a too-large refusal must not carry an answer")
}
