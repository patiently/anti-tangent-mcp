// Package blocktext holds the one rendering rule shared by every paste-ready
// text block this server emits: how a multi-line free-text value is folded so
// its continuation lines cannot be read as the block's own grammar.
//
// It is a leaf package — it imports nothing else from this repository — so
// both internal/mcpsrv (the envelope / plan / prime / extract formatters) and
// internal/planrun (the plan-run report) can depend on it without an import
// cycle. That sharing is the point: the sentinel below is load-bearing, and
// two copies of it in two packages would be free to drift apart silently.
package blocktext

import "strings"

// ContinuationSentinel is the non-whitespace marker EscapeContinuationLines
// puts at the head of every continuation line. Exported so tests can assert
// against it by name rather than by a repeated literal.
const ContinuationSentinel = "| "

// EscapeContinuationLines folds a free-text value so that no line of it after
// the first can start a line of the enclosing block's own grammar.
//
// The blocks this server renders are read line-by-line by downstream tooling
// (plugin/anti-tangent-guard/hooks/check-task-complete is the one in this
// repo): a bare "anti-tangent envelope" header line, and "label: value" lines
// matched with ^\s*label: — leading whitespace tolerated. Almost every value
// rendered into a block is free text constrained only to be non-empty
// (internal/verdict/schema.json) or caller-attested with no shape at all
// (a CodeScene skip reason, a task title), so nothing stops one from
// containing an embedded newline whose next physical line reads, verbatim,
// "anti-tangent envelope" or "verdict: pass".
//
// If s has no newline it is returned unchanged: a single-line value is always
// inlined after its own label, so it can never independently start a physical
// line — there is nothing to escape.
//
// If s spans multiple lines, every line AFTER the first is prefixed with
// contIndent followed by ContinuationSentinel. The sentinel is the
// load-bearing half: contIndent alone is whitespace, and
// "<whitespace>verdict: pass" still matches ^\s*verdict:. With the sentinel
// the line renders "<contIndent>| verdict: pass" — its first non-whitespace
// character is "|", so neither ^\s*label: nor a header check anchored at
// column 0 can match it, whatever the field contained.
//
// This is hygiene, not a security boundary. It keeps free text from
// ACCIDENTALLY forging a machine-read format; it cannot make a pasted block
// trustworthy, since the agent supplying the text can equally well type the
// block out directly. See plugin/anti-tangent-guard/README.md.
func EscapeContinuationLines(s, contIndent string) string {
	if !strings.Contains(s, "\n") {
		return s
	}
	lines := strings.Split(s, "\n")
	var b strings.Builder
	b.WriteString(lines[0])
	for _, ln := range lines[1:] {
		b.WriteString("\n")
		b.WriteString(contIndent)
		b.WriteString(ContinuationSentinel)
		b.WriteString(ln)
	}
	return b.String()
}
