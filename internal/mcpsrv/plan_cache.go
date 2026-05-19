package mcpsrv

import (
	"crypto/sha256"
	"encoding/json"
	"sync"
	"time"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

const (
	planPassCacheVersion    = "plan-pass-cache-v1"
	planPassCacheTTL        = 3 * time.Minute
	planPassCacheMaxEntries = 128
)

type planPassCacheEntry struct {
	result    verdict.PlanResult
	modelUsed string
	addedAt   time.Time
}

type planPassCache struct {
	mu      sync.Mutex
	entries map[[32]byte]planPassCacheEntry
}

func newPlanPassCache() *planPassCache {
	return &planPassCache{entries: map[[32]byte]planPassCacheEntry{}}
}

type planCachePrompt struct {
	System string `json:"system"`
	User   string `json:"user"`
}

func planPassCacheKey(planText, mode, model string, maxTokens, maxTokensOverride int, rendered renderedPlanReview) [32]byte {
	keyInput := struct {
		Version           string            `json:"version"`
		PlanText          string            `json:"plan_text"`
		Mode              string            `json:"mode"`
		Model             string            `json:"model"`
		MaxTokens         int               `json:"max_tokens"`
		MaxTokensOverride int               `json:"max_tokens_override"`
		Prompts           []planCachePrompt `json:"prompts"`
	}{
		Version:           planPassCacheVersion,
		PlanText:          planText,
		Mode:              mode,
		Model:             model,
		MaxTokens:         maxTokens,
		MaxTokensOverride: maxTokensOverride,
		Prompts:           rendered.cachePrompts(),
	}
	// keyInput contains only strings, ints, and slices of those, so JSON
	// marshaling cannot fail in practice.
	keyJSON, _ := json.Marshal(keyInput)
	return sha256.Sum256(keyJSON)
}

func (c *planPassCache) lookup(key [32]byte) (verdict.PlanResult, string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[key]
	if !ok {
		return verdict.PlanResult{}, "", false
	}
	if time.Since(entry.addedAt) > planPassCacheTTL {
		delete(c.entries, key)
		return verdict.PlanResult{}, "", false
	}
	pr := clonePlanResult(entry.result)
	pr.NextAction = "[cached <=3m] " + pr.NextAction
	pr.SummaryBlock = formatPlanSummary(pr, entry.modelUsed, 0)
	return pr, entry.modelUsed, true
}

func (c *planPassCache) store(key [32]byte, pr verdict.PlanResult, modelUsed string) {
	if pr.PlanVerdict != verdict.VerdictPass {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for k, v := range c.entries {
		if now.Sub(v.addedAt) > planPassCacheTTL {
			delete(c.entries, k)
		}
	}
	if _, exists := c.entries[key]; !exists {
		c.evictOldestUntilBelowMax()
	}
	c.entries[key] = planPassCacheEntry{
		result:    clonePlanResult(pr),
		modelUsed: modelUsed,
		addedAt:   now,
	}
}

func (c *planPassCache) evictOldestUntilBelowMax() {
	for len(c.entries) >= planPassCacheMaxEntries {
		var oldestKey [32]byte
		var oldestAddedAt time.Time
		first := true
		for k, v := range c.entries {
			if first || v.addedAt.Before(oldestAddedAt) {
				oldestKey = k
				oldestAddedAt = v.addedAt
				first = false
			}
		}
		delete(c.entries, oldestKey)
	}
}

func (c *planPassCache) entryCountForTest() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

func (c *planPassCache) expireForTest() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range c.entries {
		v.addedAt = time.Now().Add(-planPassCacheTTL - time.Second)
		c.entries[k] = v
	}
}

func clonePlanResult(pr verdict.PlanResult) verdict.PlanResult {
	pr.PlanFindings = append([]verdict.Finding(nil), pr.PlanFindings...)
	pr.Tasks = append([]verdict.PlanTaskResult(nil), pr.Tasks...)
	for i := range pr.Tasks {
		pr.Tasks[i].Findings = append([]verdict.Finding(nil), pr.Tasks[i].Findings...)
		pr.Tasks[i].ExitContracts = append([]string(nil), pr.Tasks[i].ExitContracts...)
		pr.Tasks[i].NormativeTestBodies = append([]string(nil), pr.Tasks[i].NormativeTestBodies...)
	}
	return pr
}
