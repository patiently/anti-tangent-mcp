package codescene

import (
	"encoding/json"
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
