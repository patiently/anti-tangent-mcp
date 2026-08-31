package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoad_Defaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"ANTHROPIC_API_KEY": "sk-ant-test",
	}))
	require.NoError(t, err)
	assert.Equal(t, "sk-ant-test", cfg.AnthropicKey)
	assert.Equal(t, ModelRef{Provider: "anthropic", Model: "claude-sonnet-4-6"}, cfg.PreModel)
	assert.Equal(t, ModelRef{Provider: "anthropic", Model: "claude-haiku-4-5-20251001"}, cfg.MidModel)
	assert.Equal(t, ModelRef{Provider: "anthropic", Model: "claude-opus-4-7"}, cfg.PostModel)
	assert.Equal(t, 4*time.Hour, cfg.SessionTTL)
	assert.Equal(t, 204800, cfg.MaxPayloadBytes)
	assert.Equal(t, 180*time.Second, cfg.RequestTimeout)
}

func TestLoad_Overrides(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"OPENAI_API_KEY":                 "sk-test",
		"ANTI_TANGENT_PRE_MODEL":         "openai:gpt-5",
		"ANTI_TANGENT_SESSION_TTL":       "30m",
		"ANTI_TANGENT_MAX_PAYLOAD_BYTES": "1024",
	}))
	require.NoError(t, err)
	assert.Equal(t, ModelRef{Provider: "openai", Model: "gpt-5"}, cfg.PreModel)
	assert.Equal(t, 30*time.Minute, cfg.SessionTTL)
	assert.Equal(t, 1024, cfg.MaxPayloadBytes)
}

func TestLoad_NoKeys(t *testing.T) {
	_, err := Load(env(map[string]string{}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one of")
}

func TestLoad_BadModelRef(t *testing.T) {
	_, err := Load(env(map[string]string{
		"ANTHROPIC_API_KEY":      "x",
		"ANTI_TANGENT_PRE_MODEL": "no-colon",
	}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected provider:model")
}

func TestLoad_NonPositiveTunables(t *testing.T) {
	cases := []map[string]string{
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_SESSION_TTL": "0s"},
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_SESSION_TTL": "-1m"},
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_MAX_PAYLOAD_BYTES": "0"},
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_MAX_PAYLOAD_BYTES": "-1024"},
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_REQUEST_TIMEOUT": "0s"},
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_REQUEST_TIMEOUT": "-5s"},
	}
	for _, tc := range cases {
		_, err := Load(env(tc))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be positive")
	}
}

func TestParseModelRef(t *testing.T) {
	mr, err := ParseModelRef("anthropic:claude-opus-4-7")
	require.NoError(t, err)
	assert.Equal(t, ModelRef{Provider: "anthropic", Model: "claude-opus-4-7"}, mr)
	assert.Equal(t, "anthropic:claude-opus-4-7", mr.String())

	_, err = ParseModelRef("bad")
	require.Error(t, err)
}

func TestLoad_PlanModel_DefaultsToPre(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"ANTHROPIC_API_KEY": "k",
	}))
	require.NoError(t, err)
	assert.Equal(t, cfg.PreModel, cfg.PlanModel)
}

func TestLoad_PlanModel_InheritsPreOverride(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"ANTHROPIC_API_KEY":      "k",
		"ANTI_TANGENT_PRE_MODEL": "openai:gpt-5",
	}))
	require.NoError(t, err)
	assert.Equal(t, ModelRef{Provider: "openai", Model: "gpt-5"}, cfg.PreModel)
	assert.Equal(t, cfg.PreModel, cfg.PlanModel)
}

func TestLoad_PlanModel_ExplicitOverride(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"ANTHROPIC_API_KEY":       "k",
		"ANTI_TANGENT_PRE_MODEL":  "openai:gpt-5",
		"ANTI_TANGENT_PLAN_MODEL": "google:gemini-2.5-pro",
	}))
	require.NoError(t, err)
	assert.Equal(t, ModelRef{Provider: "openai", Model: "gpt-5"}, cfg.PreModel)
	assert.Equal(t, ModelRef{Provider: "google", Model: "gemini-2.5-pro"}, cfg.PlanModel)
}

func TestLoad_TokenBudgetsAndChunkSize_Defaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"ANTHROPIC_API_KEY": "sk-ant-test",
	}))
	require.NoError(t, err)
	assert.Equal(t, 4096, cfg.PerTaskMaxTokens)
	assert.Equal(t, 4096, cfg.PlanMaxTokens)
	assert.Equal(t, 8, cfg.PlanTasksPerChunk)
}

func TestLoad_TokenBudgetsAndChunkSize_Overrides(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"ANTHROPIC_API_KEY":                 "sk-ant-test",
		"ANTI_TANGENT_PER_TASK_MAX_TOKENS":  "8192",
		"ANTI_TANGENT_PLAN_MAX_TOKENS":      "16384",
		"ANTI_TANGENT_PLAN_TASKS_PER_CHUNK": "12",
	}))
	require.NoError(t, err)
	assert.Equal(t, 8192, cfg.PerTaskMaxTokens)
	assert.Equal(t, 16384, cfg.PlanMaxTokens)
	assert.Equal(t, 12, cfg.PlanTasksPerChunk)
}

func TestLoad_MaxTokensCeiling(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    int
		wantErr bool
	}{
		{"default when unset", "", 16384, false},
		{"valid override", "32768", 32768, false},
		{"invalid string rejected", "abc", 0, true},
		{"zero rejected", "0", 0, true},
		{"negative rejected", "-1", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(func(k string) string {
				switch k {
				case "ANTHROPIC_API_KEY":
					return "k"
				case "ANTI_TANGENT_MAX_TOKENS_CEILING":
					return tt.value
				}
				return ""
			})
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, cfg.MaxTokensCeiling)
		})
	}
}

func TestLoad_TokenBudgetsAndChunkSize_Reject(t *testing.T) {
	cases := []map[string]string{
		// ANTI_TANGENT_PER_TASK_MAX_TOKENS
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_PER_TASK_MAX_TOKENS": "0"},
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_PER_TASK_MAX_TOKENS": "-1"},
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_PER_TASK_MAX_TOKENS": "not-an-int"},
		// ANTI_TANGENT_PLAN_MAX_TOKENS
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_PLAN_MAX_TOKENS": "0"},
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_PLAN_MAX_TOKENS": "-1"},
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_PLAN_MAX_TOKENS": "not-an-int"},
		// ANTI_TANGENT_PLAN_TASKS_PER_CHUNK
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_PLAN_TASKS_PER_CHUNK": "0"},
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_PLAN_TASKS_PER_CHUNK": "-1"},
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_PLAN_TASKS_PER_CHUNK": "not-an-int"},
	}
	for _, tc := range cases {
		_, err := Load(env(tc))
		require.Error(t, err)
	}
}

// --- v0.6.0 project-knowledge env vars ----------------------------------

func TestLoad_KBStore_Default(t *testing.T) {
	cfg, err := Load(env(map[string]string{"ANTHROPIC_API_KEY": "k"}))
	require.NoError(t, err)
	assert.Equal(t, "", cfg.KBStore)
}

func TestLoad_KBStore_BasicMemoryAccepted(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"ANTHROPIC_API_KEY":     "k",
		"ANTI_TANGENT_KB_STORE": "basic-memory",
	}))
	require.NoError(t, err)
	assert.Equal(t, "basic-memory", cfg.KBStore)
}

func TestLoad_KBStore_InvalidValueRejected(t *testing.T) {
	_, err := Load(env(map[string]string{
		"ANTHROPIC_API_KEY":     "k",
		"ANTI_TANGENT_KB_STORE": "bogus",
	}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ANTI_TANGENT_KB_STORE")
}

func TestLoad_PrimeAndExtractModel_FallbackToPre(t *testing.T) {
	cfg, err := Load(env(map[string]string{"ANTHROPIC_API_KEY": "k"}))
	require.NoError(t, err)
	// With no PLAN/PRIME/EXTRACT overrides, PlanModel falls back to PreModel,
	// and PrimeModel/ExtractModel chain through PlanModel to land on PreModel.
	assert.Equal(t, cfg.PreModel, cfg.PrimeModel)
	assert.Equal(t, cfg.PreModel, cfg.ExtractModel)
}

func TestLoad_PrimeAndExtractModel_FallbackToPlan(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"ANTHROPIC_API_KEY":       "k",
		"ANTI_TANGENT_PLAN_MODEL": "anthropic:claude-opus-4-7",
	}))
	require.NoError(t, err)
	assert.Equal(t, ModelRef{Provider: "anthropic", Model: "claude-opus-4-7"}, cfg.PlanModel)
	assert.Equal(t, cfg.PlanModel, cfg.PrimeModel)
	assert.Equal(t, cfg.PlanModel, cfg.ExtractModel)
	// Sanity: PlanModel did not collapse onto PreModel.
	assert.NotEqual(t, cfg.PreModel, cfg.PlanModel)
}

func TestLoad_PrimeAndExtractModel_ExplicitOverrideWins(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"ANTHROPIC_API_KEY":          "k",
		"ANTI_TANGENT_PLAN_MODEL":    "anthropic:claude-opus-4-7",
		"ANTI_TANGENT_PRIME_MODEL":   "anthropic:claude-sonnet-4-6",
		"ANTI_TANGENT_EXTRACT_MODEL": "anthropic:claude-haiku-4-5-20251001",
	}))
	require.NoError(t, err)
	assert.Equal(t, ModelRef{Provider: "anthropic", Model: "claude-sonnet-4-6"}, cfg.PrimeModel)
	assert.Equal(t, ModelRef{Provider: "anthropic", Model: "claude-haiku-4-5-20251001"}, cfg.ExtractModel)
	// PlanModel itself still reflects its own explicit override.
	assert.Equal(t, ModelRef{Provider: "anthropic", Model: "claude-opus-4-7"}, cfg.PlanModel)
}

func TestLoad_PrimeAndExtractModel_BadModelRef(t *testing.T) {
	_, err := Load(env(map[string]string{
		"ANTHROPIC_API_KEY":        "k",
		"ANTI_TANGENT_PRIME_MODEL": "no-colon",
	}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ANTI_TANGENT_PRIME_MODEL")

	_, err = Load(env(map[string]string{
		"ANTHROPIC_API_KEY":          "k",
		"ANTI_TANGENT_EXTRACT_MODEL": "no-colon",
	}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ANTI_TANGENT_EXTRACT_MODEL")
}

func TestLoad_PrimeExtractMaxTokens_Defaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{"ANTHROPIC_API_KEY": "k"}))
	require.NoError(t, err)
	assert.Equal(t, 4096, cfg.PrimeMaxTokens)
	assert.Equal(t, 8192, cfg.ExtractMaxTokens)
}

func TestLoad_PrimeExtractMaxTokens_Overrides(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"ANTHROPIC_API_KEY":               "k",
		"ANTI_TANGENT_PRIME_MAX_TOKENS":   "2048",
		"ANTI_TANGENT_EXTRACT_MAX_TOKENS": "16384",
	}))
	require.NoError(t, err)
	assert.Equal(t, 2048, cfg.PrimeMaxTokens)
	assert.Equal(t, 16384, cfg.ExtractMaxTokens)
}

func TestLoad_PrimeExtractMaxTokens_Reject(t *testing.T) {
	cases := []map[string]string{
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_PRIME_MAX_TOKENS": "0"},
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_PRIME_MAX_TOKENS": "-1"},
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_PRIME_MAX_TOKENS": "not-an-int"},
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_EXTRACT_MAX_TOKENS": "0"},
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_EXTRACT_MAX_TOKENS": "-1"},
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_EXTRACT_MAX_TOKENS": "not-an-int"},
	}
	for _, tc := range cases {
		_, err := Load(env(tc))
		require.Error(t, err)
	}
}

func TestLoad_Codescene(t *testing.T) {
	base := map[string]string{"ANTHROPIC_API_KEY": "k"}
	get := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}

	cfg, err := Load(get(base))
	require.NoError(t, err)
	assert.Equal(t, "", cfg.Codescene, "default is off")

	m := map[string]string{"ANTHROPIC_API_KEY": "k", "ANTI_TANGENT_CODESCENE": "required"}
	cfg, err = Load(get(m))
	require.NoError(t, err)
	assert.Equal(t, "required", cfg.Codescene)

	m = map[string]string{"ANTHROPIC_API_KEY": "k", "ANTI_TANGENT_CODESCENE": "optional"}
	_, err = Load(get(m))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ANTI_TANGENT_CODESCENE")
	assert.Contains(t, err.Error(), `allowed: "", "required"`)
}

func TestPlanPayloadCapAndRoots(t *testing.T) {
	base := map[string]string{"ANTHROPIC_API_KEY": "k"}
	envFrom := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}

	t.Run("defaults", func(t *testing.T) {
		cfg, err := Load(envFrom(base))
		require.NoError(t, err)
		assert.Equal(t, 1048576, cfg.PlanMaxPayloadBytes)
		assert.Equal(t, 204800, cfg.MaxPayloadBytes)
		assert.Empty(t, cfg.PlanRoots)
	})

	t.Run("plan cap override", func(t *testing.T) {
		m := map[string]string{"ANTHROPIC_API_KEY": "k", "ANTI_TANGENT_PLAN_MAX_PAYLOAD_BYTES": "4096"}
		cfg, err := Load(envFrom(m))
		require.NoError(t, err)
		assert.Equal(t, 4096, cfg.PlanMaxPayloadBytes)
		assert.Equal(t, 204800, cfg.MaxPayloadBytes, "shared cap untouched")
	})

	t.Run("plan cap rejects non-positive", func(t *testing.T) {
		m := map[string]string{"ANTHROPIC_API_KEY": "k", "ANTI_TANGENT_PLAN_MAX_PAYLOAD_BYTES": "0"}
		_, err := Load(envFrom(m))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ANTI_TANGENT_PLAN_MAX_PAYLOAD_BYTES")
	})

	t.Run("roots parsed and cleaned", func(t *testing.T) {
		// Built with the OS path-list separator (filepath.SplitList's convention)
		// rather than a literal ":" so this subtest is correct on Windows too,
		// where the separator is ";".
		rootsEnv := strings.Join([]string{"/home/a/", "/srv/b", " "}, string(os.PathListSeparator))
		m := map[string]string{"ANTHROPIC_API_KEY": "k", "ANTI_TANGENT_PLAN_ROOTS": rootsEnv}
		cfg, err := Load(envFrom(m))
		require.NoError(t, err)
		// Neither /home/a nor /srv/b exists in the test sandbox, so this also
		// pins the not-yet-created-root fallback: EvalSymlinks fails and the
		// Cleaned value is kept rather than erroring at startup.
		assert.Equal(t, []string{"/home/a", "/srv/b"}, cfg.PlanRoots)
	})

	// TestPlanPayloadCapAndRoots/"roots are symlink-resolved" is the regression
	// test for the withinRoots mismatch: internal/mcpsrv/file_source.go's
	// resolveFileInput EvalSymlinks the candidate path before checking root
	// membership, so a root that is only Cleaned (not resolved) refuses every
	// legitimate path under it whenever an ancestor of the root is itself a
	// symlink — exactly the shape of macOS's /tmp -> /private/tmp, which is
	// also what docs/protocol/implementer.md told implementers to use.
	t.Run("roots are symlink-resolved", func(t *testing.T) {
		real := t.TempDir()
		realResolved, err := filepath.EvalSymlinks(real)
		require.NoError(t, err)
		linkParent := t.TempDir()
		link := filepath.Join(linkParent, "link-root")
		require.NoError(t, os.Symlink(real, link))

		m := map[string]string{"ANTHROPIC_API_KEY": "k", "ANTI_TANGENT_PLAN_ROOTS": link}
		cfg, err := Load(envFrom(m))
		require.NoError(t, err)
		assert.Equal(t, []string{realResolved}, cfg.PlanRoots,
			"a symlinked root must resolve to the real path so it matches resolveFileInput's resolved candidate")
	})

	t.Run("relative root rejected", func(t *testing.T) {
		m := map[string]string{"ANTHROPIC_API_KEY": "k", "ANTI_TANGENT_PLAN_ROOTS": "relative/path"}
		_, err := Load(envFrom(m))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ANTI_TANGENT_PLAN_ROOTS")
		assert.Contains(t, err.Error(), "absolute")
	})

	// Regression test: ANTI_TANGENT_PLAN_ROOTS is a security-narrowing
	// setting. A set-but-empty-after-parsing value (a bare separator, or
	// whitespace-only entries) used to leave cfg.PlanRoots nil with no
	// error — which withinRoots treats as "allow everything", the exact
	// opposite of what an operator setting this variable intends. It must
	// fail closed at startup instead.
	t.Run("set but zero usable entries fails closed", func(t *testing.T) {
		for name, v := range map[string]string{
			"bare separator":      string(os.PathListSeparator),
			"whitespace only":     "   ",
			"whitespace and seps": " " + string(os.PathListSeparator) + " ",
		} {
			t.Run(name, func(t *testing.T) {
				m := map[string]string{"ANTHROPIC_API_KEY": "k", "ANTI_TANGENT_PLAN_ROOTS": v}
				_, err := Load(envFrom(m))
				require.Error(t, err)
				assert.Contains(t, err.Error(), "ANTI_TANGENT_PLAN_ROOTS")
			})
		}
	})

	// Regression test for the Windows unusability bug: parsing used to
	// hardcode ":" as the separator, so a Windows value like `C:\plans` split
	// into ["C", "\plans"] and failed the absolute-path check — no value of
	// the variable worked on that platform. Building the input with
	// os.PathListSeparator (rather than a literal ":") means this test
	// exercises the OS's real separator on every platform it runs on,
	// including a hypothetical Windows CI run where it would be ";".
	t.Run("roots parsed using the OS path-list separator", func(t *testing.T) {
		a := t.TempDir()
		b := t.TempDir()
		joined := strings.Join([]string{a, b}, string(os.PathListSeparator))
		m := map[string]string{"ANTHROPIC_API_KEY": "k", "ANTI_TANGENT_PLAN_ROOTS": joined}
		cfg, err := Load(envFrom(m))
		require.NoError(t, err)
		aResolved, err := filepath.EvalSymlinks(a)
		require.NoError(t, err)
		bResolved, err := filepath.EvalSymlinks(b)
		require.NoError(t, err)
		assert.Equal(t, []string{aResolved, bResolved}, cfg.PlanRoots)
	})
}

func TestPlanMaxPayloadBytesFollowsSharedCap(t *testing.T) {
	envFrom := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}

	t.Run("neither var set defaults to 1MB", func(t *testing.T) {
		cfg, err := Load(envFrom(map[string]string{"ANTHROPIC_API_KEY": "k"}))
		require.NoError(t, err)
		assert.Equal(t, 1048576, cfg.PlanMaxPayloadBytes)
	})

	t.Run("shared cap raised above 1MB raises the plan cap with it", func(t *testing.T) {
		m := map[string]string{"ANTHROPIC_API_KEY": "k", "ANTI_TANGENT_MAX_PAYLOAD_BYTES": "2097152"}
		cfg, err := Load(envFrom(m))
		require.NoError(t, err)
		assert.Equal(t, 2097152, cfg.MaxPayloadBytes)
		assert.Equal(t, 2097152, cfg.PlanMaxPayloadBytes,
			"an operator who already raised the shared cap must not regress on upgrade")
	})

	t.Run("explicit plan cap wins even when lower than the raised shared cap", func(t *testing.T) {
		m := map[string]string{
			"ANTHROPIC_API_KEY":                   "k",
			"ANTI_TANGENT_MAX_PAYLOAD_BYTES":      "2097152",
			"ANTI_TANGENT_PLAN_MAX_PAYLOAD_BYTES": "500000",
		}
		cfg, err := Load(envFrom(m))
		require.NoError(t, err)
		assert.Equal(t, 2097152, cfg.MaxPayloadBytes)
		assert.Equal(t, 500000, cfg.PlanMaxPayloadBytes, "explicit setting must win even if lower")
	})

	t.Run("shared cap below 1MB leaves the plan cap at the 1MB default", func(t *testing.T) {
		m := map[string]string{"ANTHROPIC_API_KEY": "k", "ANTI_TANGENT_MAX_PAYLOAD_BYTES": "102400"}
		cfg, err := Load(envFrom(m))
		require.NoError(t, err)
		assert.Equal(t, 102400, cfg.MaxPayloadBytes)
		assert.Equal(t, 1048576, cfg.PlanMaxPayloadBytes)
	})
}
