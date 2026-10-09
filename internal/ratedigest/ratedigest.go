// Package ratedigest defines the rate digest a validate_completion caller may
// send for agent-network work: the k/n counts of an eval run, the regression
// suite's result, whether the change was kept, and its rigidity delta.
//
// anti-tangent never runs an eval. Like a CodeScene digest, the rate digest is
// caller-supplied and unverified: the reviewer is told so, and the plan-run
// record keeps only its counts. It is a leaf package, so both internal/planrun
// and internal/mcpsrv can depend on it.
package ratedigest

import (
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxN          = 10000
	maxTargetEval = 300
)

// Suite is the regression suite's result.
type Suite struct {
	Evals       int `json:"evals"`
	Regressions int `json:"regressions"`
}

// RigidityDelta counts what the change added of each rigidity kind.
type RigidityDelta struct {
	OutboundStrings int `json:"outbound_strings"`
	VetoKeys        int `json:"veto_keys"`
	StateMarkers    int `json:"state_markers"`
	StrategyLines   int `json:"strategy_lines"`
}

// Digest is a validated rate digest. Absent optional fields are nil.
type Digest struct {
	TargetEval    string         `json:"target_eval,omitempty"`
	N             int            `json:"n"`
	BeforeK       *int           `json:"before_k,omitempty"`
	AfterK        *int           `json:"after_k,omitempty"`
	IntervalAfter []float64      `json:"interval_after,omitempty"`
	Suite         *Suite         `json:"suite,omitempty"`
	Kept          *bool          `json:"kept,omitempty"`
	RigidityDelta *RigidityDelta `json:"rigidity_delta,omitempty"`
}

// Parse validates raw, the rate_digest argument as JSON decoding left it,
// and returns the digest. A nil raw returns (nil, ""). A malformed digest
// returns nil and the reason, for the caller to report: it is never an
// argument error. Unknown keys are ignored.
func Parse(raw any) (*Digest, string) {
	if raw == nil {
		return nil, ""
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, "rate_digest must be an object"
	}
	p := parser{m: m}
	d := &Digest{}
	n, ok := p.count("n", 1, maxN)
	if !ok {
		return nil, p.reason("n is required: an integer from 1 to 10000")
	}
	d.N = n
	d.BeforeK = p.optionalCount("before_k", 0, n)
	d.AfterK = p.optionalCount("after_k", 0, n)
	d.TargetEval = p.targetEval()
	d.IntervalAfter = p.interval("interval_after")
	d.Suite = p.suite()
	d.Kept = p.optionalBool("kept")
	d.RigidityDelta = p.rigidity()
	if p.problem != "" {
		return nil, p.problem
	}
	return d, ""
}

type parser struct {
	m       map[string]any
	problem string
}

// reason keeps the first problem found.
func (p *parser) reason(r string) string {
	if p.problem == "" {
		p.problem = r
	}
	return p.problem
}

// integer reports v as an int when it is a whole JSON number in [lo, hi].
func integer(v any, lo, hi int) (int, bool) {
	f, ok := v.(float64)
	if !ok || f != math.Trunc(f) || f < float64(lo) || f > float64(hi) {
		return 0, false
	}
	return int(f), true
}

func (p *parser) count(key string, lo, hi int) (int, bool) {
	v, present := p.m[key]
	if !present {
		return 0, false
	}
	return integer(v, lo, hi)
}

func (p *parser) optionalCount(key string, lo, hi int) *int {
	v, present := p.m[key]
	if !present || v == nil {
		return nil
	}
	n, ok := integer(v, lo, hi)
	if !ok {
		p.reason(fmt.Sprintf("%s must be an integer from %d to n (%d)", key, lo, hi))
		return nil
	}
	return &n
}

func (p *parser) targetEval() string {
	v, present := p.m["target_eval"]
	if !present || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok || utf8.RuneCountInString(s) > maxTargetEval || strings.IndexFunc(s, unicode.IsControl) >= 0 {
		p.reason("target_eval must be a string of at most 300 characters with no control characters")
		return ""
	}
	return s
}

func (p *parser) interval(key string) []float64 {
	v, present := p.m[key]
	if !present || v == nil {
		return nil
	}
	pair, ok := v.([]any)
	if ok && len(pair) == 2 {
		lo, okLo := pair[0].(float64)
		hi, okHi := pair[1].(float64)
		if okLo && okHi && lo >= 0 && hi <= 1 && lo <= hi {
			return []float64{lo, hi}
		}
	}
	p.reason(key + " must be two numbers between 0 and 1, lower first")
	return nil
}

func (p *parser) object(key string) (map[string]any, bool) {
	v, present := p.m[key]
	if !present || v == nil {
		return nil, false
	}
	obj, ok := v.(map[string]any)
	if !ok {
		p.reason(key + " must be an object")
		return nil, false
	}
	return obj, true
}

func (p *parser) suite() *Suite {
	obj, ok := p.object("suite")
	if !ok {
		return nil
	}
	evals, okE := integer(obj["evals"], 0, math.MaxInt32)
	regressions, okR := integer(obj["regressions"], 0, math.MaxInt32)
	if !okE || !okR || regressions > evals {
		p.reason("suite needs evals and regressions: non-negative integers, regressions at most evals")
		return nil
	}
	return &Suite{Evals: evals, Regressions: regressions}
}

func (p *parser) optionalBool(key string) *bool {
	v, present := p.m[key]
	if !present || v == nil {
		return nil
	}
	b, ok := v.(bool)
	if !ok {
		p.reason(key + " must be true or false")
		return nil
	}
	return &b
}

func (p *parser) rigidity() *RigidityDelta {
	obj, ok := p.object("rigidity_delta")
	if !ok {
		return nil
	}
	var d RigidityDelta
	for key, dst := range map[string]*int{
		"outbound_strings": &d.OutboundStrings,
		"veto_keys":        &d.VetoKeys,
		"state_markers":    &d.StateMarkers,
		"strategy_lines":   &d.StrategyLines,
	} {
		v, present := obj[key]
		if !present {
			continue
		}
		n, ok := integer(v, 0, math.MaxInt32)
		if !ok {
			p.reason("rigidity_delta counts must be non-negative integers")
			return nil
		}
		*dst = n
	}
	return &d
}

// RawKept reports whether raw, the rate_digest argument as sent, records the
// change as kept. It reads the kept key alone, so a digest Parse drops as
// malformed still counts for the rule that a kept change must send a diff.
func RawKept(raw any) bool {
	m, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	kept, ok := m["kept"].(bool)
	return ok && kept
}

// IsKept reports whether the digest records the change as kept.
func (d *Digest) IsKept() bool {
	return d != nil && d.Kept != nil && *d.Kept
}

// ForRecord returns the copy a plan-run row keeps: the counts, without the
// eval's path.
func (d *Digest) ForRecord() *Digest {
	if d == nil {
		return nil
	}
	cp := *d
	cp.TargetEval = ""
	cp.IntervalAfter = append([]float64(nil), d.IntervalAfter...)
	if d.BeforeK != nil {
		b := *d.BeforeK
		cp.BeforeK = &b
	}
	if d.AfterK != nil {
		a := *d.AfterK
		cp.AfterK = &a
	}
	if d.Suite != nil {
		s := *d.Suite
		cp.Suite = &s
	}
	if d.Kept != nil {
		k := *d.Kept
		cp.Kept = &k
	}
	if d.RigidityDelta != nil {
		r := *d.RigidityDelta
		cp.RigidityDelta = &r
	}
	return &cp
}

// rate renders the before and after counts: "5→8/10", "8/10", "5→?/10".
func (d *Digest) rate() string {
	after := "?"
	if d.AfterK != nil {
		after = fmt.Sprint(*d.AfterK)
	}
	if d.BeforeK == nil {
		return fmt.Sprintf("%s/%d", after, d.N)
	}
	return fmt.Sprintf("%d→%s/%d", *d.BeforeK, after, d.N)
}

// Cell renders the plan_run_report rate column:
// "5→8/10 · reg 0 · rig +0/+0/+0 · kept", the rigidity part counting
// outbound strings, veto keys and state markers. A part the digest lacks is
// left out.
func (d *Digest) Cell() string {
	if d == nil {
		return ""
	}
	parts := []string{d.rate()}
	if d.Suite != nil {
		parts = append(parts, fmt.Sprintf("reg %d", d.Suite.Regressions))
	}
	if r := d.RigidityDelta; r != nil {
		parts = append(parts, fmt.Sprintf("rig +%d/+%d/+%d", r.OutboundStrings, r.VetoKeys, r.StateMarkers))
	}
	if d.Kept != nil {
		parts = append(parts, map[bool]string{true: "kept", false: "reverted"}[*d.Kept])
	}
	return strings.Join(parts, " · ")
}

// PromptLines renders the digest for the completion prompt, one line per
// part it carries.
func (d *Digest) PromptLines() []string {
	if d == nil {
		return nil
	}
	var out []string
	if d.TargetEval != "" {
		out = append(out, "Target eval: "+d.TargetEval)
	}
	rate := "Rate: " + d.rate()
	if len(d.IntervalAfter) == 2 {
		rate += fmt.Sprintf(" (after-rate interval %.2f–%.2f)", d.IntervalAfter[0], d.IntervalAfter[1])
	}
	out = append(out, rate)
	if d.Suite != nil {
		out = append(out, fmt.Sprintf("Regression suite: %d evals, %d regressions", d.Suite.Evals, d.Suite.Regressions))
	}
	if d.Kept != nil {
		kept := "reverted"
		if *d.Kept {
			kept = "kept"
		}
		out = append(out, "Outcome: "+kept)
	}
	if r := d.RigidityDelta; r != nil {
		out = append(out, fmt.Sprintf("Rigidity delta: outbound strings +%d, veto keys +%d, state markers +%d, strategy lines +%d",
			r.OutboundStrings, r.VetoKeys, r.StateMarkers, r.StrategyLines))
	}
	return out
}
