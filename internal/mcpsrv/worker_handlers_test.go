package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/stats"
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

// TestBulkReadRejectsRelativePathBeforeProviderCall pins AC4: a relative path
// must be rejected before any provider call, not merely rejected eventually.
// Asserting only that an error comes back would pass even if BulkRead called
// the worker first and only noticed the bad path afterward — so this also
// asserts f.calls stayed 0, which can only be true if resolveFileInput's
// IsAbs check ran and failed strictly before runWorker ever reached the
// provider.
func TestBulkReadRejectsRelativePathBeforeProviderCall(t *testing.T) {
	f := &fakeWorkerReviewer{resp: providers.Response{RawJSON: []byte(`{"answer":"x"}`)}}
	h := &handlers{deps: workerDeps(t, f, nil)}
	_, _, err := h.BulkRead(context.Background(), nil, BulkReadArgs{Question: "q", Paths: []string{"relative/path.go"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be absolute", "want the path-must-be-absolute error")
	assert.Equal(t, 0, f.calls, "provider must not be called before the relative-path check fails")
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

// "reference_path" alone is not a discriminating substring here: resolveFileInput
// also returns an error containing "reference_path" (via CodeWrite's own
// fmt.Errorf("reference_path: %w", ...) wrap around its "path is empty"
// message) for an empty path, so that substring alone would still pass even
// with the explicit up-front guard deleted. Anchoring on "context-free" —
// text unique to the guard's explanatory message — plus asserting the
// worker was never reached both pin the acceptance criterion: a rejection
// message that explains WHY context-free code is useless, not merely any
// error mentioning the field name.
func TestCodeWriteRequiresReferencePath(t *testing.T) {
	f := &fakeWorkerReviewer{}
	h := &handlers{deps: workerDeps(t, f, nil)}
	_, _, err := h.CodeWrite(context.Background(), nil, CodeWriteArgs{Spec: "make a test"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "context-free", "want the explicit reference_path guard's message, not just the generic downstream wrap")
	assert.Equal(t, 0, f.calls, "worker must not be reached when reference_path is missing")
}

func TestCodeWriteOversizedReferenceNamesSizeCapAndRemedy(t *testing.T) {
	dir := t.TempDir()
	ref := filepath.Join(dir, "big.go")
	require.NoError(t, os.WriteFile(ref, make([]byte, 5000), 0o644))

	f := &fakeWorkerReviewer{}
	d := workerDeps(t, f, []string{dir})
	d.Cfg.MaxPayloadBytes = 100
	h := &handlers{deps: d}
	_, _, err := h.CodeWrite(context.Background(), nil, CodeWriteArgs{Spec: "a func", ReferencePath: ref})

	require.Error(t, err)
	// Assert the three ACTIONABLE values, not merely that an error happened.
	// The message this replaced was "reference_path: file exceeds cap", which
	// already contains "reference_path" and "exceeds cap" — so asserting either
	// of those would pass against the very behaviour this test exists to pin.
	// Size and cap tell the caller how far over it is; the env var tells it
	// what to do. Drop any one and this fails.
	msg := err.Error()
	assert.Contains(t, msg, "5000", "must name the reference file's true size")
	assert.Contains(t, msg, "100", "must name the cap that was exceeded")
	assert.Contains(t, msg, "ANTI_TANGENT_MAX_PAYLOAD_BYTES", "must name the remedy")
	assert.Equal(t, 0, f.calls, "worker must not be reached for an oversized reference")
}

func TestCodeWriteWritesAndHidesCode(t *testing.T) {
	skipIfWriteTargetUnsupported(t)
	dir := t.TempDir()
	ref := filepath.Join(dir, "ref.go")
	require.NoError(t, os.WriteFile(ref, []byte("package ref\n"), 0o644))
	target := filepath.Join(dir, "out.go")
	f := &fakeWorkerReviewer{resp: providers.Response{
		RawJSON: []byte(`{"code":"package out\n\nfunc A() {}\n"}`), InputTokens: 5, OutputTokens: 3,
	}}
	h := &handlers{deps: workerDeps(t, f, []string{dir})}
	_, res, err := h.CodeWrite(context.Background(), nil, CodeWriteArgs{
		Spec: "a func", ReferencePath: ref, TargetPath: target,
	})
	require.NoError(t, err)

	assert.Empty(t, res.Code, "a targeted write must NOT return the code")
	assert.Equal(t, target, res.Written)
	assert.Equal(t, 3, res.LinesWritten)

	b, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "package out\n\nfunc A() {}\n", string(b))
}

func TestCodeWriteWithoutTargetReturnsCode(t *testing.T) {
	dir := t.TempDir()
	ref := filepath.Join(dir, "ref.go")
	require.NoError(t, os.WriteFile(ref, []byte("package ref\n"), 0o644))
	f := &fakeWorkerReviewer{resp: providers.Response{RawJSON: []byte(`{"code":"func A() {}"}`)}}
	h := &handlers{deps: workerDeps(t, f, []string{dir})}
	_, res, err := h.CodeWrite(context.Background(), nil, CodeWriteArgs{Spec: "s", ReferencePath: ref})
	require.NoError(t, err)

	assert.Equal(t, "func A() {}", res.Code)
	assert.Empty(t, res.Written)
}

func TestCodeWriteTruncationWritesNothing(t *testing.T) {
	dir := t.TempDir()
	ref := filepath.Join(dir, "ref.go")
	require.NoError(t, os.WriteFile(ref, []byte("package ref\n"), 0o644))
	target := filepath.Join(dir, "out.go")
	f := &fakeWorkerReviewer{err: providers.ErrResponseTruncated}
	h := &handlers{deps: workerDeps(t, f, []string{dir})}
	_, _, err := h.CodeWrite(context.Background(), nil, CodeWriteArgs{
		Spec: "s", ReferencePath: ref, TargetPath: target,
	})
	require.Error(t, err, "want an error on truncation")

	_, statErr := os.Stat(target)
	assert.True(t, os.IsNotExist(statErr), "a truncated response must not create the target file")
}

// Named so `-run CodeWrite` selects it. The code_write half of the catalog
// assertion lives here, not in Task 4: this is the task that registers it.
func TestCodeWriteRegisteredInCatalog(t *testing.T) {
	assert.True(t, catalogHas(t, "code_write"), `tool "code_write" is not registered`)
}

func TestCodeWriteEmptyOutputIsAnErrorAndWritesNothing(t *testing.T) {
	dir := t.TempDir()
	ref := filepath.Join(dir, "ref.go")
	require.NoError(t, os.WriteFile(ref, []byte("package ref\n"), 0o644))

	// Built with json.Marshal, NOT hand-written escapes: a hand-rolled
	// "```go\\n```" decodes to a literal backslash-n, so stripFences sees no
	// newline, leaves the text non-empty, and the case silently stops testing
	// what it claims to.
	mustBody := func(code string) []byte {
		b, err := json.Marshal(map[string]string{"code": code})
		require.NoError(t, err)
		return b
	}
	cases := map[string][]byte{
		"empty":      mustBody(""),
		"whitespace": mustBody("   \n\t"),
		"fence only": mustBody("```go\n```"),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			target := filepath.Join(dir, "out_"+strings.ReplaceAll(name, " ", "_")+".go")
			f := &fakeWorkerReviewer{resp: providers.Response{RawJSON: body}}
			h := &handlers{deps: workerDeps(t, f, []string{dir})}
			_, _, err := h.CodeWrite(context.Background(), nil, CodeWriteArgs{
				Spec: "s", ReferencePath: ref, TargetPath: target,
			})
			require.Error(t, err, "empty generated code must be an error")

			_, statErr := os.Stat(target)
			assert.True(t, os.IsNotExist(statErr), "nothing must be written when the worker returns no code")
		})
	}
}

// commitFailTarget wraps a real write transaction and reports a Commit
// failure, so the "finalize errors are reported, not discarded" criterion has
// something to assert against.
//
// It embeds writeTarget rather than *os.File so the wrapped transaction is
// the real one: Discard still runs the production cleanup, which is the half
// of the behaviour these tests actually assert on. Commit is failed WITHOUT
// delegating to the real Commit, which is the faithful simulation — a
// finalize that fails is one that did not rename.
type commitFailTarget struct {
	writeTarget
	err error
}

func (c commitFailTarget) Commit() error { return c.err }

func TestCodeWriteReportsCommitError(t *testing.T) {
	skipIfWriteTargetUnsupported(t)
	dir := t.TempDir()
	ref := filepath.Join(dir, "ref.go")
	require.NoError(t, os.WriteFile(ref, []byte("package ref\n"), 0o644))

	orig := openWriteTarget
	t.Cleanup(func() { openWriteTarget = orig })
	openWriteTarget = func(target string, roots []string, overwrite bool) (writeTarget, error) {
		f, err := resolveWriteTarget(target, roots, overwrite)
		if err != nil {
			return nil, err
		}
		return commitFailTarget{writeTarget: f, err: errors.New("disk went away")}, nil
	}

	f := &fakeWorkerReviewer{resp: providers.Response{RawJSON: []byte(`{"code":"package out\n"}`)}}
	h := &handlers{deps: workerDeps(t, f, []string{dir})}
	target := filepath.Join(dir, "out.go")
	_, _, err := h.CodeWrite(context.Background(), nil, CodeWriteArgs{
		Spec: "s", ReferencePath: ref, TargetPath: target,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disk went away", "a Close error must surface")

	// A failed commit must not leave a file behind either. This is the create
	// path (no overwrite), so O_EXCL already claimed the name: whatever
	// WriteString put there is a file this call itself brought into
	// existence, and it is an orphaned partial unless Discard removes it.
	_, statErr := os.Stat(target)
	assert.True(t, os.IsNotExist(statErr), "a failed commit must not leave a partial file behind")
}

// writeFailTarget wraps a real write transaction and reports a WriteString
// failure, so the "a failed write does not leave a damaged file behind"
// property has something to assert against. Disk-full during the write is the likelier
// real-world failure — more likely than Close failing — and unlike Close,
// nothing exercised this branch before.
type writeFailTarget struct {
	writeTarget
	err error
}

func (w writeFailTarget) WriteString(string) (int, error) { return 0, w.err }

func TestCodeWriteWriteFailureLeavesNoFileBehind(t *testing.T) {
	skipIfWriteTargetUnsupported(t)
	dir := t.TempDir()
	ref := filepath.Join(dir, "ref.go")
	require.NoError(t, os.WriteFile(ref, []byte("package ref\n"), 0o644))
	target := filepath.Join(dir, "out.go")

	orig := openWriteTarget
	t.Cleanup(func() { openWriteTarget = orig })
	openWriteTarget = func(target string, roots []string, overwrite bool) (writeTarget, error) {
		f, err := resolveWriteTarget(target, roots, overwrite)
		if err != nil {
			return nil, err
		}
		return writeFailTarget{writeTarget: f, err: errors.New("disk full")}, nil
	}

	f := &fakeWorkerReviewer{resp: providers.Response{RawJSON: []byte(`{"code":"package out\n"}`)}}
	h := &handlers{deps: workerDeps(t, f, []string{dir})}
	_, _, err := h.CodeWrite(context.Background(), nil, CodeWriteArgs{
		Spec: "s", ReferencePath: ref, TargetPath: target,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disk full", "a WriteString error must surface")

	// This is the assertion that pins the cleanup fix: resolveWriteTarget
	// already created the file at open time, before WriteString ever ran.
	// Without an explicit removal on this failure path, a zero-length file
	// is left sitting at target_path with an error that never mentions it.
	_, statErr := os.Stat(target)
	assert.True(t, os.IsNotExist(statErr), "a failed write must not leave a partial file behind")
}

// TestCodeWriteFailedOverwritePreservesOriginal is the destruction proof for
// the atomic-overwrite contract: when overwrite is true and the write fails
// partway, target_path must still hold EXACTLY the bytes it held before the
// call.
//
// Against an implementation that opens the target with O_TRUNC this fails, and
// it fails in the worst possible way: the truncate happens at open(2), before
// WriteString ever runs, so the caller's file is already empty by the time the
// failure is noticed and the cleanup path then removes what is left. The
// assertion below is byte-identity, not merely "the file still exists" —
// "exists" would also be satisfied by a zero-length file, which is precisely
// the damage being guarded against.
func TestCodeWriteFailedOverwritePreservesOriginal(t *testing.T) {
	skipIfWriteTargetUnsupported(t)
	dir := t.TempDir()
	ref := filepath.Join(dir, "ref.go")
	require.NoError(t, os.WriteFile(ref, []byte("package ref\n"), 0o644))

	target := filepath.Join(dir, "out.go")
	const original = "package out\n\n// the caller's existing file\nfunc Existing() {}\n"
	require.NoError(t, os.WriteFile(target, []byte(original), 0o644))

	orig := openWriteTarget
	t.Cleanup(func() { openWriteTarget = orig })
	openWriteTarget = func(target string, roots []string, overwrite bool) (writeTarget, error) {
		f, err := resolveWriteTarget(target, roots, overwrite)
		if err != nil {
			return nil, err
		}
		return writeFailTarget{writeTarget: f, err: errors.New("disk full")}, nil
	}

	f := &fakeWorkerReviewer{resp: providers.Response{RawJSON: []byte(`{"code":"package out\n"}`)}}
	h := &handlers{deps: workerDeps(t, f, []string{dir})}
	_, _, err := h.CodeWrite(context.Background(), nil, CodeWriteArgs{
		Spec: "s", ReferencePath: ref, TargetPath: target, Overwrite: true,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disk full", "a WriteString error must surface")

	b, readErr := os.ReadFile(target)
	require.NoError(t, readErr, "a failed overwrite must not remove the caller's file")
	assert.Equal(t, original, string(b), "a failed overwrite must leave the original byte-identical")
	assertNoTempLeftBehind(t, dir, "ref.go", "out.go")
}

// TestCodeWriteFailedCommitPreservesOriginal is the same proof one step later
// in the sequence: the bytes were written successfully and the finalize step
// is what failed. This is the branch a temp-file design has to get right —
// finalizing is where the rename lives, so a design that renamed first and
// checked afterwards would pass the WriteString test above and still destroy
// the file here.
func TestCodeWriteFailedCommitPreservesOriginal(t *testing.T) {
	skipIfWriteTargetUnsupported(t)
	dir := t.TempDir()
	ref := filepath.Join(dir, "ref.go")
	require.NoError(t, os.WriteFile(ref, []byte("package ref\n"), 0o644))

	target := filepath.Join(dir, "out.go")
	const original = "package out\n\n// the caller's existing file\nfunc Existing() {}\n"
	require.NoError(t, os.WriteFile(target, []byte(original), 0o644))

	orig := openWriteTarget
	t.Cleanup(func() { openWriteTarget = orig })
	openWriteTarget = func(target string, roots []string, overwrite bool) (writeTarget, error) {
		f, err := resolveWriteTarget(target, roots, overwrite)
		if err != nil {
			return nil, err
		}
		return commitFailTarget{writeTarget: f, err: errors.New("disk went away")}, nil
	}

	f := &fakeWorkerReviewer{resp: providers.Response{RawJSON: []byte(`{"code":"package out\n"}`)}}
	h := &handlers{deps: workerDeps(t, f, []string{dir})}
	_, _, err := h.CodeWrite(context.Background(), nil, CodeWriteArgs{
		Spec: "s", ReferencePath: ref, TargetPath: target, Overwrite: true,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disk went away")

	b, readErr := os.ReadFile(target)
	require.NoError(t, readErr, "a failed finalize must not remove the caller's file")
	assert.Equal(t, original, string(b), "a failed finalize must leave the original byte-identical")
	assertNoTempLeftBehind(t, dir, "ref.go", "out.go")
}

func TestStripFences(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"wrapped with lang", "```go\nfunc A() {}\n```", "func A() {}\n"},
		{"wrapped bare", "```\nfunc A() {}\n```", "func A() {}\n"},
		{"trailing newline after fence", "```go\nfunc A() {}\n```\n", "func A() {}\n"},
		{"not wrapped", "func A() {}\n", "func A() {}\n"},
		{"fence mid-body is preserved", "func A() {\n// ```\n}\n", "func A() {\n// ```\n}\n"},
		{"opening fence only", "```go\nfunc A() {}\n", "```go\nfunc A() {}\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, stripFences(c.in))
		})
	}
}

func TestCountLines(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{{"", 0}, {"a\n", 1}, {"a\nb\n", 2}, {"a\nb", 2}, {"\n", 1}}
	for _, c := range cases {
		assert.Equal(t, c.want, countLines(c.in), "countLines(%q)", c.in)
	}
}

func TestBulkReadRecordsEvent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(p, []byte("package a\n"), 0o644))

	statsDir := t.TempDir()
	rec, err := stats.New(stats.Options{
		Dir:              statsDir,
		SummaryInterval:  24 * time.Hour,
		SummaryThreshold: 1000,
		RetentionDays:    30,
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	require.NoError(t, err)

	f := &fakeWorkerReviewer{resp: providers.Response{
		RawJSON: []byte(`{"answer":"- a.go: package a"}`), Model: "anthropic:claude-haiku-4-5-20251001",
		InputTokens: 42, OutputTokens: 7,
	}}
	deps := workerDeps(t, f, []string{dir})
	deps.Stats = rec
	h := &handlers{deps: deps}
	_, res, err := h.BulkRead(context.Background(), nil, BulkReadArgs{Question: "what package?", Paths: []string{p}})
	require.NoError(t, err)
	assert.Equal(t, 42, res.InputTokens)
	assert.Equal(t, 7, res.OutputTokens)

	// Assert exactly one event was recorded with the right tool and tokens.
	events, err := readEventsFromDir(statsDir)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "bulk_read", events[0].Tool)
	assert.Equal(t, 42, events[0].InputTokens)
	assert.Equal(t, 7, events[0].OutputTokens)
	assert.Equal(t, "anthropic:claude-haiku-4-5-20251001", events[0].Model)
}

func TestBulkReadRecordingWithNilStats(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(p, []byte("package a\n"), 0o644))

	f := &fakeWorkerReviewer{resp: providers.Response{
		RawJSON: []byte(`{"answer":"- a.go: package a"}`), Model: "anthropic:claude-haiku-4-5-20251001",
		InputTokens: 42, OutputTokens: 7,
	}}
	deps := workerDeps(t, f, []string{dir})
	deps.Stats = nil // explicitly nil
	h := &handlers{deps: deps}
	_, res, err := h.BulkRead(context.Background(), nil, BulkReadArgs{Question: "what package?", Paths: []string{p}})
	require.NoError(t, err)
	assert.Equal(t, 42, res.InputTokens)
	// Verify the call succeeds even with Stats: nil and doesn't panic.
}

func TestCodeWriteRecordsEvent(t *testing.T) {
	skipIfWriteTargetUnsupported(t)
	dir := t.TempDir()
	ref := filepath.Join(dir, "ref.go")
	require.NoError(t, os.WriteFile(ref, []byte("package ref\n"), 0o644))
	target := filepath.Join(dir, "new.go")

	statsDir := t.TempDir()
	rec, err := stats.New(stats.Options{
		Dir:              statsDir,
		SummaryInterval:  24 * time.Hour,
		SummaryThreshold: 1000,
		RetentionDays:    30,
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	require.NoError(t, err)

	f := &fakeWorkerReviewer{resp: providers.Response{
		RawJSON: []byte(`{"code":"package out\n"}`), Model: "anthropic:claude-haiku-4-5-20251001",
		InputTokens: 100, OutputTokens: 50,
	}}
	deps := workerDeps(t, f, []string{dir})
	deps.Stats = rec
	h := &handlers{deps: deps}
	_, res, err := h.CodeWrite(context.Background(), nil, CodeWriteArgs{
		Spec: "spec", ReferencePath: ref, TargetPath: target,
	})
	require.NoError(t, err)
	assert.Equal(t, 100, res.InputTokens)
	assert.Equal(t, 50, res.OutputTokens)

	// Assert exactly one event was recorded with the right tool and tokens.
	events, err := readEventsFromDir(statsDir)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "code_write", events[0].Tool)
	assert.Equal(t, 100, events[0].InputTokens)
	assert.Equal(t, 50, events[0].OutputTokens)
	assert.Equal(t, "anthropic:claude-haiku-4-5-20251001", events[0].Model)
}

func TestCodeWriteRecordingWithNilStats(t *testing.T) {
	skipIfWriteTargetUnsupported(t)
	dir := t.TempDir()
	ref := filepath.Join(dir, "ref.go")
	require.NoError(t, os.WriteFile(ref, []byte("package ref\n"), 0o644))
	target := filepath.Join(dir, "new.go")

	f := &fakeWorkerReviewer{resp: providers.Response{
		RawJSON: []byte(`{"code":"package out\n"}`), Model: "anthropic:claude-haiku-4-5-20251001",
		InputTokens: 100, OutputTokens: 50,
	}}
	deps := workerDeps(t, f, []string{dir})
	deps.Stats = nil // explicitly nil
	h := &handlers{deps: deps}
	_, res, err := h.CodeWrite(context.Background(), nil, CodeWriteArgs{
		Spec: "spec", ReferencePath: ref, TargetPath: target,
	})
	require.NoError(t, err)
	assert.Equal(t, 100, res.InputTokens)
	// Verify the call succeeds even with Stats: nil and doesn't panic.
}

func TestCodeWriteRecordsEventEvenIfWriteFails(t *testing.T) {
	skipIfWriteTargetUnsupported(t)
	dir := t.TempDir()
	ref := filepath.Join(dir, "ref.go")
	require.NoError(t, os.WriteFile(ref, []byte("package ref\n"), 0o644))
	target := filepath.Join(dir, "new.go")

	statsDir := t.TempDir()
	rec, err := stats.New(stats.Options{
		Dir:              statsDir,
		SummaryInterval:  24 * time.Hour,
		SummaryThreshold: 1000,
		RetentionDays:    30,
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	require.NoError(t, err)

	f := &fakeWorkerReviewer{resp: providers.Response{
		RawJSON: []byte(`{"code":"package out\n"}`), Model: "anthropic:claude-haiku-4-5-20251001",
		InputTokens: 100, OutputTokens: 50,
	}}
	deps := workerDeps(t, f, []string{dir})
	deps.Stats = rec
	h := &handlers{deps: deps}

	// Force a write failure.
	oldOpenWriteTarget := openWriteTarget
	defer func() { openWriteTarget = oldOpenWriteTarget }()
	openWriteTarget = func(target string, roots []string, overwrite bool) (writeTarget, error) {
		f, err := resolveWriteTarget(target, roots, overwrite)
		if err != nil {
			return nil, err
		}
		return writeFailTarget{writeTarget: f, err: errors.New("disk full")}, nil
	}

	_, _, err = h.CodeWrite(context.Background(), nil, CodeWriteArgs{
		Spec: "spec", ReferencePath: ref, TargetPath: target,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disk full")

	// The event should still be recorded despite the write failure.
	events, err := readEventsFromDir(statsDir)
	require.NoError(t, err)
	require.Len(t, events, 1, "event must be recorded even if the write fails")
	assert.Equal(t, "code_write", events[0].Tool)
	assert.Equal(t, 100, events[0].InputTokens)
}

// readEventsFromDir reads the events.jsonl file from a stats directory.
// This is a helper for tests only.
func readEventsFromDir(dir string) ([]stats.Event, error) {
	b, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		return nil, err
	}
	var events []stats.Event
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var ev stats.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return nil, err
		}
		events = append(events, ev)
	}
	return events, nil
}
