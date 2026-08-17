package codescene

import (
	"encoding/json"
	"math"
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

func TestNormalize_CapsCategoryKeyLength(t *testing.T) {
	// Well under codesceneCategoryCountsMax entries, so the entry-count cap
	// alone would let this through untouched. The key-length cap must fire
	// independently of the entry-count cap.
	longKey := strings.Repeat("x", 500)
	d := Digest{CategoryCounts: map[string]int{longKey: 5}}
	d.Normalize()
	for k := range d.CategoryCounts {
		assert.LessOrEqual(t, len([]rune(k)), codesceneCategoryKeyMaxRunes+1, "truncated key (plus ellipsis) must not exceed the cap")
		assert.True(t, strings.HasSuffix(k, "…"), "over-cap key must be marked truncated")
	}
	assert.Equal(t, 5, d.CategoryCounts[truncateRunes(longKey, codesceneCategoryKeyMaxRunes)])
}

func TestNormalize_CategoryKeyCollisionSumsCounts(t *testing.T) {
	prefix := strings.Repeat("y", codesceneCategoryKeyMaxRunes)
	d := Digest{CategoryCounts: map[string]int{
		prefix + "-first":  3,
		prefix + "-second": 4,
	}}
	d.Normalize()
	assert.Equal(t, 1, len(d.CategoryCounts), "both long keys truncate to the same prefix")
	assert.Equal(t, 7, d.CategoryCounts[truncateRunes(prefix+"-first", codesceneCategoryKeyMaxRunes)])
}

// Two distinct keys can truncate to the same key, at which point their
// caller-supplied counts are summed. A plain + would wrap to a negative and
// reorder (or drop) entries in the top-N cap; saturating keeps the ordering
// meaningful. Regression for the overflow CodeRabbit flagged on PR #59.
func TestCapCategoryCounts_CollidingKeysSaturateInsteadOfWrapping(t *testing.T) {
	long := strings.Repeat("a", 120) // exceeds the 100-rune key cap, so both keys truncate to the same prefix
	d := Digest{CategoryCounts: map[string]int{
		long + "-one": math.MaxInt - 1,
		long + "-two": math.MaxInt - 1,
		"small":       3,
	}}
	d.Normalize()

	for k, v := range d.CategoryCounts {
		assert.GreaterOrEqual(t, v, 0, "key %q wrapped negative", k)
	}
	var merged int
	for k, v := range d.CategoryCounts {
		if k != "small" {
			merged = v
		}
	}
	assert.Equal(t, math.MaxInt, merged, "colliding counts must saturate, not wrap")
}
