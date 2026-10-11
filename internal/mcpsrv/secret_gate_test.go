package mcpsrv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/secretgate"
)

func gateDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return d
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	return p
}

func pemBody() string {
	return "-----BEGIN " + "RSA PRIVATE" + " KEY-----\n" + strings.Repeat("MIIE", 16) + "\n"
}

func TestSecretGate_TaskSpecContextPathRefused(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("m")}
	h := &handlers{deps: newDeps(t, rv)}
	env := writeFile(t, gateDir(t), ".env", "A=1\n")
	_, out, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"}, ContextPaths: []string{env},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "secret gate")
	assert.Contains(t, err.Error(), "dotenv")
	assert.Empty(t, out.SessionID)
	assert.Equal(t, 0, rv.Calls)
}

func TestSecretGate_CompletionInputsRefused(t *testing.T) {
	dir := gateDir(t)
	cases := map[string]func(*ValidateCompletionArgs){
		"context_paths": func(a *ValidateCompletionArgs) { a.ContextPaths = []string{writeFile(t, dir, ".env", "A=1\n")} },
		"final_files": func(a *ValidateCompletionArgs) {
			a.FinalFiles = []CompletionFileArg{{Path: writeFile(t, dir, "k.txt", pemBody())}}
		},
		"final_diff_path": func(a *ValidateCompletionArgs) {
			a.FinalDiffPath = writeFile(t, dir, "d.diff", "+key = "+"AKIA"+strings.Repeat("Q", 16)+"\n")
		},
		"test_evidence_path": func(a *ValidateCompletionArgs) { a.TestEvidencePath = writeFile(t, dir, ".env.local", "ok\n") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			rv := &fakeReviewer{name: "anthropic", resp: passResp("m")}
			h := &handlers{deps: newDeps(t, rv)}
			args := completionCallArgs("")
			mutate(&args)
			_, _, err := h.ValidateCompletion(context.Background(), nil, args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "secret gate")
			assert.Equal(t, 0, rv.Calls)
		})
	}
}

func TestSecretGate_SymlinkToSecretRefused(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("m")}
	h := &handlers{deps: newDeps(t, rv)}
	dir := gateDir(t)
	target := writeFile(t, dir, ".env", "A=1\n")
	link := filepath.Join(dir, "notes.txt")
	require.NoError(t, os.Symlink(target, link))
	_, _, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"}, ContextPaths: []string{link},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dotenv")
}

func TestSecretGate_OffLetsTheFileThrough(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("m")}
	h := &handlers{deps: newDeps(t, rv)}
	h.deps.Cfg.SecretGate = nil
	env := writeFile(t, gateDir(t), ".env", "A=1\n")
	_, _, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"}, ContextPaths: []string{env},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, rv.Calls)
}

func TestSecretGate_PlanPathRefused(t *testing.T) {
	h := newTestPlanHandlers(t)
	p := writeFile(t, gateDir(t), ".env", buildPlanWithNTasks(1))
	_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanPath: p})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "secret gate")
}

func TestSecretGate_BulkReadRefused(t *testing.T) {
	f := &fakeWorkerReviewer{resp: providers.Response{RawJSON: []byte(`{"answer":"x"}`)}}
	d := workerDeps(t, f, nil)
	gate, err := secretgate.New(secretgate.ModeOn, nil)
	require.NoError(t, err)
	d.Cfg.SecretGate = gate
	h := &handlers{deps: d}
	p := writeFile(t, gateDir(t), ".env", "A=1\n")
	_, _, err = h.BulkRead(context.Background(), nil, BulkReadArgs{Question: "q", Paths: []string{p}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "secret gate")
	assert.Equal(t, 0, f.calls)
}
