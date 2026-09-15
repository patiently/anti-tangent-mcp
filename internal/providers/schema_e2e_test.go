//go:build e2e

package providers

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// TestPerTaskSchema_E2E_NullableSameAs sends the per-task reviewer schema to
// each provider whose API key is set and checks that the response parses with
// same_as set on one finding and null on another. same_as is a type array that
// includes null, which each provider's structured-output mode must accept.
//
// Gated on ANTI_TANGENT_E2E_SCHEMA=1 as well as the e2e build tag: one live
// call per provider with a key. Run with:
//
//	ANTI_TANGENT_E2E_SCHEMA=1 ANTHROPIC_API_KEY=… OPENAI_API_KEY=… GOOGLE_API_KEY=… \
//	  go test -tags=e2e -count=1 -run TestPerTaskSchema_E2E_NullableSameAs ./internal/providers/ -v
func TestPerTaskSchema_E2E_NullableSameAs(t *testing.T) {
	if os.Getenv("ANTI_TANGENT_E2E_SCHEMA") != "1" {
		t.Skip("set ANTI_TANGENT_E2E_SCHEMA=1 to enable (one live call per provider with a key; costs real money)")
	}
	cases := []struct {
		provider, keyEnv, model string
		newReviewer             func(key string) Reviewer
	}{
		{"anthropic", "ANTHROPIC_API_KEY", "claude-haiku-4-5-20251001", func(k string) Reviewer { return NewAnthropic(k, "", 120*time.Second) }},
		{"openai", "OPENAI_API_KEY", "gpt-5-mini", func(k string) Reviewer { return NewOpenAI(k, "", 120*time.Second) }},
		{"google", "GOOGLE_API_KEY", "gemini-2.5-flash", func(k string) Reviewer { return NewGoogle(k, "", 120*time.Second) }},
	}
	prompt := "This is a schema conformance check, not a real review. Return verdict warn, next_action \"none\", and exactly two findings: " +
		"first a minor quality finding with criterion \"first\", evidence \"e\", suggestion \"s\" and same_as \"f_0123abcd\"; " +
		"then a minor quality finding with criterion \"second\", evidence \"e\", suggestion \"s\" and same_as null."

	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			key := os.Getenv(tc.keyEnv)
			if key == "" {
				t.Skipf("%s is not set", tc.keyEnv)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
			defer cancel()

			resp, err := tc.newReviewer(key).Review(ctx, Request{
				Model:      tc.model,
				System:     "You return ONLY a JSON object matching the provided schema.",
				User:       prompt,
				MaxTokens:  4096,
				JSONSchema: verdict.Schema(),
			})
			require.NoError(t, err, "%s rejected or failed the per-task schema", tc.provider)

			r, err := verdict.Parse(resp.RawJSON)
			require.NoError(t, err, "raw: %s", resp.RawJSON)
			require.Len(t, r.Findings, 2, "raw: %s", resp.RawJSON)
			require.NotNil(t, r.Findings[0].SameAs, "raw: %s", resp.RawJSON)
			assert.Equal(t, "f_0123abcd", *r.Findings[0].SameAs)
			assert.Nil(t, r.Findings[1].SameAs, "raw: %s", resp.RawJSON)
		})
	}
}
