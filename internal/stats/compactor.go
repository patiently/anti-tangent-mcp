package stats

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/patiently/anti-tangent-mcp/internal/providers"
)

// summarySchema constrains the reviewer to a single prose field. All providers
// require a non-empty JSONSchema and force JSON output, so we cannot request
// free prose directly — we ask for {"summary": "..."} and extract it.
const summarySchema = `{"type":"object","properties":{"summary":{"type":"string"}},"required":["summary"],"additionalProperties":false}`

const summarySystemPrompt = "You are an operations analyst. Given aggregate, anonymized statistics about an advisory code-review tool's own activity, write a brief (3-6 sentence) descriptive operational report: verdict mix, finding density and dominant categories, latency, model usage, cache/partial rates, and the trend vs the previous window if provided. This tool is advisory and has NO ground truth on whether findings were correct or acted upon — do NOT claim findings were right, wrong, useful, or ignored. Respond with a JSON object: {\"summary\": \"<markdown>\"}."

type summaryResponse struct {
	Summary string `json:"summary"`
}

type summaryRecord struct {
	Ts          time.Time `json:"ts"`
	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"`
	Text        string    `json:"text"`
}

// Compactor computes the rollup and (when a reviewer is configured) the prose
// summary. It is stateless beyond its config; the Recorder owns event-file I/O
// and snapshots events in before calling Compact.
type Compactor struct {
	dir       string
	reviewer  providers.Reviewer // nil => summary step skipped
	model     string
	maxTokens int
	timeout   time.Duration
	logger    *slog.Logger
}

// Compact writes rollup.json from events, then (if a reviewer is configured)
// asks for a narrative and writes summary.md + appends summaries.jsonl.
// Best-effort: every error is logged and swallowed; rollup.json is always
// attempted before the LLM step so machine stats stay fresh when it fails.
func (c *Compactor) Compact(now time.Time, events []Event, csEvents []CodesceneEvent) {
	rollup := computeRollup(events, now)
	if cs := computeCodescene(csEvents, now); cs != nil {
		rollup.Codescene = cs
	}
	if err := writeJSON(c.dir, rollupFile, rollup); err != nil {
		c.logger.Warn("stats rollup write failed", "err", err)
	}
	if c.reviewer == nil {
		return
	}
	prompt := buildSummaryPrompt(rollup, c.readPrevSummary())
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	resp, err := c.reviewer.Review(ctx, providers.Request{
		Model:      c.model,
		System:     summarySystemPrompt,
		User:       prompt,
		MaxTokens:  c.maxTokens,
		JSONSchema: []byte(summarySchema),
	})
	if err != nil {
		c.logger.Warn("stats summary skipped (reviewer error)", "err", err)
		return
	}
	var sr summaryResponse
	if err := json.Unmarshal(resp.RawJSON, &sr); err != nil || strings.TrimSpace(sr.Summary) == "" {
		c.logger.Warn("stats summary unparseable", "err", err)
		return
	}
	if err := os.WriteFile(filepath.Join(c.dir, summaryMDFile), []byte(sr.Summary), 0o644); err != nil {
		c.logger.Warn("stats summary.md write failed", "err", err)
		return
	}
	if err := appendJSONL(c.dir, summariesFile, summaryRecord{
		Ts: now, WindowStart: rollup.WindowStart, WindowEnd: rollup.WindowEnd, Text: sr.Summary,
	}); err != nil {
		c.logger.Warn("stats summaries.jsonl append failed", "err", err)
	}
}

func (c *Compactor) readPrevSummary() string {
	b, err := os.ReadFile(filepath.Join(c.dir, summaryMDFile))
	if err != nil {
		return ""
	}
	return string(b)
}

func buildSummaryPrompt(r Rollup, prev string) string {
	b, _ := json.MarshalIndent(r, "", "  ")
	var sb strings.Builder
	fmt.Fprintf(&sb, "Window: %s to %s\n\nRollup (JSON):\n%s\n",
		r.WindowStart.Format(time.RFC3339), r.WindowEnd.Format(time.RFC3339), string(b))
	if strings.TrimSpace(prev) != "" {
		fmt.Fprintf(&sb, "\nPrevious window's summary (for trend comparison):\n%s\n", prev)
	}
	return sb.String()
}
