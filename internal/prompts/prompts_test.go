package prompts

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/session"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

var update = flag.Bool("update", false, "update golden files")

func sampleSpec() session.TaskSpec {
	return session.TaskSpec{
		Title: "Add /healthz endpoint",
		Goal:  "Liveness probe for the HTTP server",
		AcceptanceCriteria: []string{
			"Returns 200 OK with body \"ok\"",
			"Responds in under 50ms p95",
		},
		NonGoals: []string{"Database health (covered separately)"},
		Context:  "Service is a Gin app on port 8080.",
	}
}

func golden(t *testing.T, name string, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		require.NoError(t, os.WriteFile(path, []byte(got), 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(want), got)
}

func TestRenderPre(t *testing.T) {
	out, err := RenderPre(PreInput{Spec: sampleSpec()})
	require.NoError(t, err)
	golden(t, "pre_basic", out.System+"\n---USER---\n"+out.User)
}

func TestRenderMid(t *testing.T) {
	out, err := RenderMid(MidInput{
		Spec: sampleSpec(),
		PriorFindings: []verdict.Finding{{
			Severity:   verdict.SeverityMajor,
			Category:   verdict.CategoryAmbiguousSpec,
			Criterion:  "AC #2",
			Evidence:   "\"under 50ms\" — at what load?",
			Suggestion: "Pin the load profile (RPS).",
		}},
		WorkingOn: "writing the handler",
		Files: []File{{
			Path:    "handlers/health.go",
			Content: "package handlers\nfunc Health(c *gin.Context) { c.String(200, \"ok\") }\n",
		}},
		Questions: []string{"Should we expose this on a separate port?"},
	})
	require.NoError(t, err)
	golden(t, "mid_basic", out.System+"\n---USER---\n"+out.User)
}

func TestRenderPost(t *testing.T) {
	out, err := RenderPost(PostInput{
		Spec:    sampleSpec(),
		Summary: "Added Gin handler at /healthz returning \"ok\".",
		Files: []File{{
			Path:    "handlers/health.go",
			Content: "package handlers\nfunc Health(c *gin.Context) { c.String(200, \"ok\") }\n",
		}},
		TestEvidence: "PASS: TestHealthReturns200",
	})
	require.NoError(t, err)
	golden(t, "post_basic", out.System+"\n---USER---\n"+out.User)
}
