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

func TestNormalizeBoundsSkipEvidence(t *testing.T) {
	d := &Digest{SkipEvidence: "  " + strings.Repeat("é", 2500) + "  "}
	d.Normalize()
	// max RETAINED runes plus the ellipsis: truncateRunes appends the marker
	// beyond the cap rather than inside it, matching SkipReason's behaviour.
	got := []rune(d.SkipEvidence)
	if len(got) != codesceneSkipEvidenceMaxRunes+1 {
		t.Fatalf("got %d runes, want %d (cap plus ellipsis)",
			len(got), codesceneSkipEvidenceMaxRunes+1)
	}
	if got[len(got)-1] != '…' {
		t.Errorf("truncated value does not end in an ellipsis: %q", string(got[len(got)-3:]))
	}
	if got[0] != 'é' {
		t.Errorf("leading whitespace was not trimmed: %q", string(got[0]))
	}
}

func TestSkipReasonCapUnchanged(t *testing.T) {
	assert.Equal(t, 300, codesceneSkipReasonMaxRunes)

	d := &Digest{SkipReason: strings.Repeat("r", 400)}
	d.Normalize()
	assert.Equal(t, codesceneSkipReasonMaxRunes+1, len([]rune(d.SkipReason)),
		"SkipReason must still truncate at its own cap, not at SkipEvidence's")
}

func TestSkipEvidenceJSONContract(t *testing.T) {
	b, err := json.Marshal(&Digest{SkipEvidence: "MCP error: tool not found"})
	require.NoError(t, err)
	assert.Contains(t, string(b), `"skip_evidence":"MCP error: tool not found"`)

	b, err = json.Marshal(&Digest{})
	require.NoError(t, err)
	assert.NotContains(t, string(b), "skip_evidence",
		"the field must be omitted when empty, not emitted as an empty string")
}

func TestDigest_UnmarshalJSON_ReducesRawChangeSet(t *testing.T) {
	raw := `{"quality_gates":"passed","results":[
		{"name":"a.go","verdict":"improved","findings":[{"category":"Complex Method","new-pp":1.0,"old-pp":2.5}]},
		{"name":"b.go","verdict":"stable","findings":[]},
		{"name":"c.go","verdict":"degraded","findings":[{"category":"Complex Method","new-pp":3.0,"old-pp":2.0},{"category":"Bumpy Road Ahead","new-pp":1.0,"old-pp":0.0}]}
	]}`
	var d Digest
	require.NoError(t, json.Unmarshal([]byte(raw), &d))
	assert.True(t, d.Ran)
	assert.Equal(t, "analyze_change_set", d.Tool)
	assert.Equal(t, "passed", d.QualityGate)
	assert.Equal(t, 3, d.FilesAnalyzed)
	require.NotNil(t, d.Verdicts)
	assert.Equal(t, Verdicts{Improved: 1, Degraded: 1, Stable: 1}, *d.Verdicts)
	assert.InDelta(t, 0.5, d.NetPP, 1e-9)
	assert.Equal(t, map[string]int{"Complex Method": 2, "Bumpy Road Ahead": 1}, d.CategoryCounts)
}

func TestDigest_UnmarshalJSON_EmptyChangeSetStillRan(t *testing.T) {
	var d Digest
	require.NoError(t, json.Unmarshal([]byte(`{"quality_gates":"passed","results":[]}`), &d))
	assert.True(t, d.Ran)
	assert.Equal(t, 0, d.FilesAnalyzed)
	require.NotNil(t, d.Verdicts)
	assert.Equal(t, Verdicts{}, *d.Verdicts)
}

func TestDigest_UnmarshalJSON_PresentDigestFieldsWin(t *testing.T) {
	raw := `{"ran":false,"skip_reason":"tool errored","quality_gate":"failed","quality_gates":"passed",
		"files_analyzed":7,"verdicts":{"improved":0,"degraded":2,"stable":5},"net_pp":4,
		"results":[{"verdict":"improved","findings":[{"category":"X","new-pp":0,"old-pp":9}]}]}`
	var d Digest
	require.NoError(t, json.Unmarshal([]byte(raw), &d))
	assert.False(t, d.Ran)
	assert.Equal(t, "failed", d.QualityGate)
	assert.Equal(t, 7, d.FilesAnalyzed)
	assert.Equal(t, Verdicts{Degraded: 2, Stable: 5}, *d.Verdicts)
	assert.InDelta(t, 4.0, d.NetPP, 1e-9)
	assert.Equal(t, map[string]int{"X": 1}, d.CategoryCounts, "category_counts was absent, so it is derived")
}

func TestDigest_UnmarshalJSON_SentToolWins(t *testing.T) {
	var empty Digest
	require.NoError(t, json.Unmarshal([]byte(`{"tool":"","quality_gates":"passed","results":[]}`), &empty))
	assert.Equal(t, "", empty.Tool, "a tool key the caller sent wins over the derived value, even when empty")

	var named Digest
	require.NoError(t, json.Unmarshal([]byte(`{"tool":"codescene-cli","results":[]}`), &named))
	assert.Equal(t, "codescene-cli", named.Tool)
}

func TestDigest_UnmarshalJSON_IgnoresUnknownAndMistypedRawKeys(t *testing.T) {
	raw := `{"ran":true,"base_ref":"abc123","note":"free text","results":{"not":"a list"},
		"verdicts":{"improved":9,"degraded":0,"stable":3,"unknown_or_no_findings":17}}`
	var d Digest
	require.NoError(t, json.Unmarshal([]byte(raw), &d))
	assert.True(t, d.Ran)
	assert.Equal(t, 0, d.FilesAnalyzed)
	assert.Equal(t, Verdicts{Improved: 9, Stable: 3}, *d.Verdicts)
}

func TestDigest_UnmarshalJSON_MistypedRawKeyLeavesTheOtherReduced(t *testing.T) {
	var badGate Digest
	require.NoError(t, json.Unmarshal([]byte(`{"quality_gates":true,"results":[
		{"verdict":"degraded","findings":[{"category":"Complex Method","new-pp":2,"old-pp":1}]}]}`), &badGate))
	assert.True(t, badGate.Ran)
	assert.Equal(t, "analyze_change_set", badGate.Tool)
	assert.Equal(t, "", badGate.QualityGate)
	assert.Equal(t, 1, badGate.FilesAnalyzed)
	require.NotNil(t, badGate.Verdicts)
	assert.Equal(t, Verdicts{Degraded: 1}, *badGate.Verdicts)
	assert.InDelta(t, 1.0, badGate.NetPP, 1e-9)
	assert.Equal(t, map[string]int{"Complex Method": 1}, badGate.CategoryCounts)

	var badResults Digest
	require.NoError(t, json.Unmarshal([]byte(`{"quality_gates":"failed","results":"n/a"}`), &badResults))
	assert.True(t, badResults.Ran)
	assert.Equal(t, "failed", badResults.QualityGate)
	assert.Equal(t, 0, badResults.FilesAnalyzed)
	assert.Nil(t, badResults.Verdicts)
}

func TestDigest_UnmarshalJSON_NullQualityGatesIsAbsent(t *testing.T) {
	var d Digest
	require.NoError(t, json.Unmarshal([]byte(`{"quality_gates": null}`), &d))
	assert.False(t, d.Ran)
	assert.Equal(t, "", d.QualityGate)
}

func TestDigest_UnmarshalJSON_DigestShapeRoundTrips(t *testing.T) {
	want := Digest{Ran: true, Tool: "analyze_change_set", QualityGate: "passed", FilesAnalyzed: 2,
		Verdicts: &Verdicts{Improved: 1, Stable: 1}, Trend: TrendImprovement, NetPP: -1,
		CategoryCounts: map[string]int{"Complex Method": 1}}
	b, err := json.Marshal(want)
	require.NoError(t, err)
	var got Digest
	require.NoError(t, json.Unmarshal(b, &got))
	assert.Equal(t, want, got)
}
