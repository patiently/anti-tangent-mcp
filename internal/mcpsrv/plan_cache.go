package mcpsrv

import (
	"crypto/sha256"
	"encoding/json"
	"sync"
	"time"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

const (
	planPassCacheVersion = "plan-pass-cache-v1"
	planPassCacheTTL     = 3 * time.Minute
)

type planPassCacheEntry struct {
	result    verdict.PlanResult
	modelUsed string
	expiresAt time.Time
}

var planPassCache = struct {
	mu      sync.Mutex
	entries map[[32]byte]planPassCacheEntry
}{entries: map[[32]byte]planPassCacheEntry{}}

type planCachePrompt struct {
	System string `json:"system"`
	User   string `json:"user"`
}

func planPassCacheKey(planText, mode, model string, maxTokens int, rendered renderedPlanReview) [32]byte {
	keyInput := struct {
		Version   string            `json:"version"`
		PlanText  string            `json:"plan_text"`
		Mode      string            `json:"mode"`
		Model     string            `json:"model"`
		MaxTokens int               `json:"max_tokens"`
		Prompts   []planCachePrompt `json:"prompts"`
	}{
		Version:   planPassCacheVersion,
		PlanText:  planText,
		Mode:      mode,
		Model:     model,
		MaxTokens: maxTokens,
		Prompts:   rendered.cachePrompts(),
	}
	keyJSON, _ := json.Marshal(keyInput)
	return sha256.Sum256(keyJSON)
}

func lookupPlanPassCache(key [32]byte) (verdict.PlanResult, string, bool) {
	planPassCache.mu.Lock()
	defer planPassCache.mu.Unlock()

	entry, ok := planPassCache.entries[key]
	if !ok {
		return verdict.PlanResult{}, "", false
	}
	if time.Now().After(entry.expiresAt) {
		delete(planPassCache.entries, key)
		return verdict.PlanResult{}, "", false
	}
	pr := entry.result
	pr.NextAction = "[cached <=3m] " + pr.NextAction
	pr.SummaryBlock = formatPlanSummary(pr, entry.modelUsed, 0)
	return pr, entry.modelUsed, true
}

func storePlanPassCache(key [32]byte, pr verdict.PlanResult, modelUsed string) {
	if pr.PlanVerdict != verdict.VerdictPass {
		return
	}

	planPassCache.mu.Lock()
	defer planPassCache.mu.Unlock()
	now := time.Now()
	for k, v := range planPassCache.entries {
		if now.After(v.expiresAt) {
			delete(planPassCache.entries, k)
		}
	}
	planPassCache.entries[key] = planPassCacheEntry{
		result:    pr,
		modelUsed: modelUsed,
		expiresAt: now.Add(planPassCacheTTL),
	}
}

func resetPlanPassCacheForTest() {
	planPassCache.mu.Lock()
	defer planPassCache.mu.Unlock()
	planPassCache.entries = map[[32]byte]planPassCacheEntry{}
}

func expirePlanPassCacheForTest() {
	planPassCache.mu.Lock()
	defer planPassCache.mu.Unlock()
	for k, v := range planPassCache.entries {
		v.expiresAt = time.Now().Add(-time.Second)
		planPassCache.entries[k] = v
	}
}
