package mcpsrv

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func writeEvidence(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	return p
}

const scoreboard = "| eval | before | after |\n|---|---|---|\n| zip-missing | 5/10 | 8/10 |\n\nok  \tevals\t4.2s\n"

func TestValidateCompletion_TestEvidencePathIsRead(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	h := &handlers{deps: newDeps(t, rv)}
	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		Summary: "kept at 8/10", TestEvidencePath: writeEvidence(t, "scoreboard.md", scoreboard),
	})
	require.NoError(t, err)
	require.Equal(t, 1, rv.Calls)
	require.Contains(t, rv.LastRequest.User, "| zip-missing | 5/10 | 8/10 |")
	require.Equal(t, "pass", env.Verdict)
}

func TestValidateCompletion_TestEvidencePathRunsTheNoRunGuard(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	h := &handlers{deps: newDeps(t, rv)}
	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		Summary: "s", TestEvidencePath: writeEvidence(t, "out.txt", "?   \tevals\t[no test files]\n"),
	})
	require.NoError(t, err)
	require.Contains(t, findingCategories(env.Findings), verdict.CategoryInsufficientEvidence)
	require.Equal(t, "test_evidence", env.Findings[0].Criterion)
}

func TestValidateCompletion_TestEvidencePathExclusiveWithTestEvidence(t *testing.T) {
	h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: passResp("m")})}
	_, _, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		Summary: "s", TestEvidence: "ok", TestEvidencePath: writeEvidence(t, "out.txt", "ok"),
	})
	require.EqualError(t, err, "test_evidence and test_evidence_path are mutually exclusive")
}

func TestValidateCompletion_TestEvidencePathOverItsCap(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("m")}
	d := newDeps(t, rv)
	d.Cfg.TestEvidenceMaxBytes = 16
	d.Cfg.MaxPayloadBytes = 1 << 20
	h := &handlers{deps: d}
	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		Summary: "s", TestEvidencePath: writeEvidence(t, "big.md", strings.Repeat("x", 17)),
	})
	require.NoError(t, err)
	require.Zero(t, rv.Calls)
	require.Equal(t, verdict.CategoryTooLarge, env.Findings[0].Category)
	assert.Contains(t, env.Findings[0].Evidence, "exceeds cap 16")
	assert.Contains(t, env.Findings[0].Suggestion, "Attach the Markdown scoreboard, or a per-eval summary")
	assert.Contains(t, env.Findings[0].Suggestion, "ANTI_TANGENT_TEST_EVIDENCE_MAX_BYTES")
}

func TestValidateCompletion_TestEvidencePathIsNotCountedInThePayloadCap(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("m")}
	d := newDeps(t, rv)
	d.Cfg.MaxPayloadBytes = 64
	h := &handlers{deps: d}
	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		Summary: "s", TestEvidencePath: writeEvidence(t, "board.md", strings.Repeat("ok 1 passed\n", 20)),
	})
	require.NoError(t, err)
	require.Equal(t, 1, rv.Calls)
	require.NotContains(t, findingCategories(env.Findings), verdict.CategoryTooLarge)
}

func TestValidateCompletion_TestEvidencePathEmptyFile(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("m")}
	h := &handlers{deps: newDeps(t, rv)}
	empty := writeEvidence(t, "empty.md", "")

	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{Summary: "s", TestEvidencePath: empty})
	require.NoError(t, err)
	require.Zero(t, rv.Calls, "the empty file is the only evidence")
	require.Equal(t, verdict.CategoryMalformedEvidence, env.Findings[0].Category)
	require.Contains(t, env.Findings[0].Evidence, "test_evidence_path resolved to 0 bytes")

	_, env, err = h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		Summary: "s", TestEvidencePath: empty, FinalDiff: "--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n",
	})
	require.NoError(t, err)
	require.Equal(t, 1, rv.Calls)
	require.Equal(t, verdict.CategoryInsufficientEvidence, env.Findings[0].Category)
	require.Contains(t, env.Findings[0].Evidence, "test_evidence_path resolved to 0 bytes")
}

func TestValidateCompletion_TestEvidencePathOutsideRoots(t *testing.T) {
	d := newDeps(t, &fakeReviewer{name: "anthropic", resp: passResp("m")})
	d.Cfg.PlanRoots = []string{t.TempDir()}
	h := &handlers{deps: d}
	_, _, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		Summary: "s", TestEvidencePath: writeEvidence(t, "out.md", "ok"),
	})
	require.ErrorContains(t, err, "test_evidence_path")
}

func TestExtract_TestEvidencePath(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: extractPassResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	args := extractArgs()
	args.CompletionEnvelopes[0].TestEvidence = ""
	args.CompletionEnvelopes[0].TestEvidencePath = writeEvidence(t, "board.md", scoreboard)
	_, _, err := h.ExtractProjectKnowledge(context.Background(), nil, args)
	require.NoError(t, err)
	require.Contains(t, rv.LastRequest.User, "| zip-missing | 5/10 | 8/10 |")

	h.deps.Cfg.TestEvidenceMaxBytes = 8
	args = extractArgs()
	args.CompletionEnvelopes[0].TestEvidence = ""
	args.CompletionEnvelopes[0].TestEvidencePath = writeEvidence(t, "board.md", scoreboard)
	_, r, err := h.ExtractProjectKnowledge(context.Background(), nil, args)
	require.NoError(t, err)
	require.Equal(t, verdict.CategoryTooLarge, r.Findings[0].Category)
	require.Equal(t, "completion_envelopes[0].test_evidence_path", r.Findings[0].Criterion)

	args = extractArgs()
	args.CompletionEnvelopes[0].TestEvidencePath = writeEvidence(t, "board.md", scoreboard)
	_, _, err = h.ExtractProjectKnowledge(context.Background(), nil, args)
	require.ErrorContains(t, err, "test_evidence and test_evidence_path are mutually exclusive")
}

func TestCheckEvidenceShape_YAMLDocumentEnd(t *testing.T) {
	yamlHunk := "diff --git a/e.yaml b/e.yaml\n--- a/e.yaml\n+++ b/e.yaml\n@@ -1,1 +1,2 @@\n case: zip\n"
	jsonHunk := "diff --git a/e.json b/e.json\n--- a/e.json\n+++ b/e.json\n@@ -1,1 +1,2 @@\n {\n"
	cases := []struct {
		name   string
		diff   string
		files  []FileArg
		reject bool
	}{
		{name: "document end added in a yaml diff", diff: yamlHunk + "+...\n"},
		{name: "document end added in a yml diff", diff: strings.ReplaceAll(yamlHunk, "e.yaml", "e.yml") + "+...\n"},
		{name: "ellipsis added in a json diff", diff: jsonHunk + "+...\n", reject: true},
		{name: "truncation marker in a yaml diff", diff: yamlHunk + "+# [truncated]\n", reject: true},
		{name: "yaml file with a document end", files: []FileArg{{Path: "evals/core/zip.yaml", Content: "case: zip\n...\n---\ncase: consent\n"}}},
		{name: "upper-case yml extension", files: []FileArg{{Path: "evals/ZIP.YML", Content: "case: zip\n...\n"}}},
		{name: "json file with an ellipsis", files: []FileArg{{Path: "evals/zip.json", Content: "{\n...\n}\n"}}, reject: true},
		{name: "yaml file with another marker", files: []FileArg{{Path: "e.yaml", Content: "a: 1\n// snip\n"}}, reject: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason, _ := checkEvidenceShape(tc.diff, tc.files)
			if tc.reject {
				assert.NotEmpty(t, reason, "must reject")
			} else {
				assert.Empty(t, reason, "must accept")
			}
		})
	}
}

func TestExtract_TestEvidencePathsTogetherOverThePayloadCap(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: extractPassResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	board := strings.Repeat(scoreboard, 20)
	args := extractArgs()
	args.CompletionEnvelopes[0].TestEvidence = ""
	args.CompletionEnvelopes[0].TestEvidencePath = writeEvidence(t, "board.md", board)
	second := args.CompletionEnvelopes[0]
	second.TestEvidencePath = writeEvidence(t, "board2.md", board)
	args.CompletionEnvelopes = append(args.CompletionEnvelopes, second)
	argsBytes, _ := json.Marshal(args)
	h.deps.Cfg.MaxPayloadBytes = 2*len(board) - 1
	h.deps.Cfg.TestEvidenceMaxBytes = len(board)
	require.Less(t, len(argsBytes), h.deps.Cfg.MaxPayloadBytes, "the arguments alone are under the cap")

	_, r, err := h.ExtractProjectKnowledge(context.Background(), nil, args)
	require.NoError(t, err)
	require.Zero(t, rv.Calls, "rejected before review")
	require.Equal(t, verdict.CategoryTooLarge, r.Findings[0].Category)
	require.Equal(t, "completion_envelopes[].test_evidence_path", r.Findings[0].Criterion)
	require.Contains(t, r.Findings[0].Suggestion, "ANTI_TANGENT_MAX_PAYLOAD_BYTES")
	require.NotContains(t, r.Findings[0].Suggestion, "ANTI_TANGENT_TEST_EVIDENCE_MAX_BYTES")
}
