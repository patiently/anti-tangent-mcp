package stats

import (
	"fmt"
	"strings"

	"github.com/patiently/anti-tangent-mcp/scorecard"
)

const revisitLeadPrefix = "Cycle review due:"

// revisitLead is the plain-text first line of summary.md when signals exist.
// It is plain text because the daemon renders the summary escaped, and it is
// capped at three signals because the daemon cuts the summary at 600 characters.
func revisitLead(sigs []scorecard.Signal) string {
	if len(sigs) == 0 {
		return ""
	}
	noun := "signals"
	if len(sigs) == 1 {
		noun = "signal"
	}
	items := make([]string, 0, 4)
	for i, s := range sigs {
		if i == 3 {
			items = append(items, fmt.Sprintf("+%d more", len(sigs)-3))
			break
		}
		items = append(items, revisitItem(s))
	}
	return fmt.Sprintf("%s %d %s — %s", revisitLeadPrefix, len(sigs), noun, strings.Join(items, "; "))
}

func revisitItem(s scorecard.Signal) string {
	if s.Kind == scorecard.RevisitNewModel {
		return "new model " + s.Model
	}
	c := fmt.Sprintf("%s · %s / %s", s.Cohort.Kind, s.Cohort.ImplementerModel, s.Cohort.ReviewModel)
	if s.Source != "" {
		c += " · " + s.Source
	}
	return fmt.Sprintf("%s %s (%s)", s.Kind, s.Direction, c)
}

// stripRevisitLead removes a lead line written by revisitLead, so the previous
// summary fed back to the model never carries a stale lead.
func stripRevisitLead(s string) string {
	if !strings.HasPrefix(s, revisitLeadPrefix) {
		return s
	}
	_, rest, _ := strings.Cut(s, "\n")
	return strings.TrimLeft(rest, "\n")
}

// summaryMarkdown is the summary.md body: the revisit lead, when signals
// exist, above the LLM text.
func summaryMarkdown(text string, sc *scorecard.Scorecard) string {
	if lead := revisitLead(scorecardSignals(sc)); lead != "" {
		return lead + "\n\n" + text
	}
	return text
}

func scorecardSignals(sc *scorecard.Scorecard) []scorecard.Signal {
	if sc == nil || sc.Revisit == nil {
		return nil
	}
	return *sc.Revisit
}
