package codescene

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalize_RecomputesTrendFromNetPP(t *testing.T) {
	// A caller reporting an improvement while submitting positive problem
	// points is corrected, not trusted.
	d := Digest{Ran: true, NetPP: 2.0, Trend: "improvement"}
	d.Normalize()
	assert.Equal(t, "regression", d.Trend)
}

func TestNormalize_NegativeIsImprovement(t *testing.T) {
	d := Digest{Ran: true, NetPP: -1.5}
	d.Normalize()
	assert.Equal(t, "improvement", d.Trend)
}

func TestNormalize_ZeroIsNeutral(t *testing.T) {
	d := Digest{Ran: true, NetPP: 0, Trend: "regression"}
	d.Normalize()
	assert.Equal(t, "neutral", d.Trend)
}

func TestNormalize_LowercasesQualityGate(t *testing.T) {
	d := Digest{Ran: true, QualityGate: "FAILED"}
	d.Normalize()
	assert.Equal(t, "failed", d.QualityGate)
}

func TestDigest_OmitsRanAndSkipReasonWhenZero(t *testing.T) {
	b, err := json.Marshal(Digest{QualityGate: "passed"})
	require.NoError(t, err)
	assert.NotContains(t, string(b), "ran")
	assert.NotContains(t, string(b), "skip_reason")
}

func TestNormalize_UnrecognizedQualityGateMapsToPlaceholder(t *testing.T) {
	d := Digest{Ran: true, QualityGate: "maybe"}
	d.Normalize()
	assert.Equal(t, "unrecognized", d.QualityGate)
}

func TestNormalize_EmptyQualityGateStaysEmpty(t *testing.T) {
	d := Digest{Ran: true}
	d.Normalize()
	assert.Equal(t, "", d.QualityGate)
}

func TestNormalize_TrimsAndCapsSkipReason(t *testing.T) {
	d := Digest{SkipReason: "  " + strings.Repeat("x", 400) + "  "}
	d.Normalize()
	assert.LessOrEqual(t, len([]rune(d.SkipReason)), codesceneSkipReasonMaxRunes+1, "truncated SkipReason (plus ellipsis) must not exceed the cap")
	assert.True(t, strings.HasSuffix(d.SkipReason, "…"), "over-cap SkipReason must be marked truncated")
	assert.False(t, strings.HasPrefix(d.SkipReason, " "), "SkipReason must be trimmed")
}

func TestNormalize_ShortSkipReasonUntouched(t *testing.T) {
	d := Digest{SkipReason: "docs-only task"}
	d.Normalize()
	assert.Equal(t, "docs-only task", d.SkipReason)
}

func TestNormalize_CapsCategoryCounts(t *testing.T) {
	counts := make(map[string]int, 30)
	for i := 0; i < 30; i++ {
		counts[string(rune('a'+i))] = 30 - i // distinct counts, deterministic ranking
	}
	d := Digest{CategoryCounts: counts}
	d.Normalize()
	assert.LessOrEqual(t, len(d.CategoryCounts), codesceneCategoryCountsMax)
	// The highest-count entry ("a": 30) must survive the cap.
	assert.Equal(t, 30, d.CategoryCounts["a"])
}

func TestNormalize_SmallCategoryCountsUntouched(t *testing.T) {
	d := Digest{CategoryCounts: map[string]int{"complexity": 2}}
	d.Normalize()
	assert.Equal(t, map[string]int{"complexity": 2}, d.CategoryCounts)
}
