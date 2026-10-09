package ratedigest

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// decode turns a JSON literal into what the MCP SDK hands the handler.
func decode(t *testing.T, s string) any {
	t.Helper()
	var v any
	require.NoError(t, json.Unmarshal([]byte(s), &v))
	return v
}

const fullDigest = `{"target_eval":"evals/core/zip-missing.yaml","n":10,"before_k":5,"after_k":8,
	"interval_after":[0.49,0.94],"suite":{"evals":42,"regressions":0},"kept":true,
	"rigidity_delta":{"outbound_strings":0,"veto_keys":0,"state_markers":0,"strategy_lines":3},"extra":"ignored"}`

func TestParse_Full(t *testing.T) {
	d, problem := Parse(decode(t, fullDigest))
	require.Empty(t, problem)
	require.Equal(t, "evals/core/zip-missing.yaml", d.TargetEval)
	require.Equal(t, 10, d.N)
	require.Equal(t, 5, *d.BeforeK)
	require.Equal(t, 8, *d.AfterK)
	require.Equal(t, []float64{0.49, 0.94}, d.IntervalAfter)
	require.Equal(t, &Suite{Evals: 42, Regressions: 0}, d.Suite)
	require.True(t, d.IsKept())
	require.Equal(t, &RigidityDelta{StrategyLines: 3}, d.RigidityDelta)
	require.Equal(t, "5→8/10 · reg 0 · rig +0/+0/+0 · kept", d.Cell())
	require.Equal(t, []string{
		"Target eval: evals/core/zip-missing.yaml",
		"Rate: 5→8/10 (after-rate interval 0.49–0.94)",
		"Regression suite: 42 evals, 0 regressions",
		"Outcome: kept",
		"Rigidity delta: outbound strings +0, veto keys +0, state markers +0, strategy lines +3",
	}, d.PromptLines())
}

func TestParse_Minimal(t *testing.T) {
	d, problem := Parse(decode(t, `{"n":10,"after_k":7,"kept":false}`))
	require.Empty(t, problem)
	require.Nil(t, d.BeforeK)
	require.False(t, d.IsKept())
	require.Equal(t, "7/10 · reverted", d.Cell())
	require.Equal(t, []string{"Rate: 7/10", "Outcome: reverted"}, d.PromptLines())
}

func TestParse_Absent(t *testing.T) {
	d, problem := Parse(nil)
	require.Nil(t, d)
	require.Empty(t, problem)
	require.False(t, d.IsKept())
	require.Empty(t, d.Cell())
}

func TestParse_Malformed(t *testing.T) {
	for name, in := range map[string]string{
		"not an object":             `"5/10"`,
		"n missing":                 `{"after_k":3}`,
		"n zero":                    `{"n":0}`,
		"n too large":               `{"n":10001}`,
		"n fractional":              `{"n":10.5}`,
		"n a string":                `{"n":"10"}`,
		"k above n":                 `{"n":10,"after_k":11}`,
		"k negative":                `{"n":10,"before_k":-1}`,
		"k fractional":              `{"n":10,"after_k":7.5}`,
		"target eval too long":      `{"n":10,"target_eval":"` + strings.Repeat("e", 301) + `"}`,
		"target eval newline":       `{"n":10,"target_eval":"a\nb"}`,
		"interval one value":        `{"n":10,"interval_after":[0.5]}`,
		"interval out of range":     `{"n":10,"interval_after":[0.5,1.5]}`,
		"interval reversed":         `{"n":10,"interval_after":[0.9,0.1]}`,
		"suite not object":          `{"n":10,"suite":3}`,
		"suite regressions > evals": `{"n":10,"suite":{"evals":1,"regressions":2}}`,
		"kept not bool":             `{"n":10,"kept":"yes"}`,
		"rigidity negative":         `{"n":10,"rigidity_delta":{"veto_keys":-1}}`,
	} {
		t.Run(name, func(t *testing.T) {
			d, problem := Parse(decode(t, in))
			require.Nil(t, d)
			require.NotEmpty(t, problem)
		})
	}
}

func TestForRecord_DropsThePathAndCopies(t *testing.T) {
	d, _ := Parse(decode(t, fullDigest))
	rec := d.ForRecord()
	require.Empty(t, rec.TargetEval)
	require.Equal(t, d.Cell(), rec.Cell())
	*rec.AfterK = 1
	rec.Suite.Regressions = 9
	rec.IntervalAfter[0] = 0
	require.Equal(t, 8, *d.AfterK)
	require.Equal(t, 0, d.Suite.Regressions)
	require.Equal(t, 0.49, d.IntervalAfter[0])
	var nilDigest *Digest
	require.Nil(t, nilDigest.ForRecord())
}

func TestRawKept(t *testing.T) {
	require.True(t, RawKept(decode(t, `{"n":10,"kept":true}`)))
	require.True(t, RawKept(decode(t, `{"n":10,"after_k":11,"kept":true}`)), "a malformed digest still reports kept")
	require.False(t, RawKept(decode(t, `{"n":10,"kept":false}`)))
	require.False(t, RawKept(decode(t, `{"n":10,"kept":"yes"}`)))
	require.False(t, RawKept(decode(t, `"kept"`)))
	require.False(t, RawKept(nil))
}
