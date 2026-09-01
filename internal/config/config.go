// Package config loads runtime configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	AnthropicKey      string
	OpenAIKey         string
	GoogleKey         string
	PreModel          ModelRef
	MidModel          ModelRef
	PostModel         ModelRef
	PlanModel         ModelRef
	PrimeModel        ModelRef
	ExtractModel      ModelRef
	SessionTTL        time.Duration
	MaxPayloadBytes   int
	RequestTimeout    time.Duration
	LogLevel          slog.Level
	PerTaskMaxTokens  int
	PlanMaxTokens     int
	PrimeMaxTokens    int
	ExtractMaxTokens  int
	PlanTasksPerChunk int
	MaxTokensCeiling  int
	// PlanMaxPayloadBytes caps plan content + project_knowledge for
	// validate_plan only. Separate from MaxPayloadBytes because validate_plan
	// gained ~10x headroom with plan_path while the other tools did not; a
	// 1MB plan is ~270K tokens, which the chunked path sends on every call,
	// so the cap stays a real guard. See design §4.
	PlanMaxPayloadBytes int
	// PlanRoots restricts which directories file-path inputs may be read
	// from. Empty (the default) means unrestricted — the server is stdio-only
	// and the calling agent already has unrestricted file read, so the server
	// acquires no capability the caller lacks. See design §3.1 / §3.2.
	PlanRoots []string
	// ContextMaxFileBytes caps each individual file attached to
	// validate_plan via context_paths. ContextMaxPayloadBytes caps the
	// attached set as a whole. Both are separate from PlanMaxPayloadBytes so
	// an oversized attachment set is refused with its own actionable
	// message rather than being blamed on the plan. Oversized attachments
	// are REFUSED, never truncated: the reviewer is told attached files are
	// complete, and a silently-shortened file turns that promise into a
	// source of false contradicted_codebase_claim findings. See design §3.2.
	ContextMaxFileBytes    int
	ContextMaxPayloadBytes int
	// Stats subsystem (opt-in; see spec 2026-06-02). StatsDir == "" disables
	// it entirely.
	StatsDir              string
	StatsModel            ModelRef
	StatsSummaryInterval  time.Duration
	StatsSummaryThreshold int
	StatsRetentionDays    int
	StatsMaxTokens        int
	// PlanLedger enables the durable plan-run ledger. Requires StatsDir; on
	// its own it does nothing. Separate from the stats opt-in because
	// plan-runs.jsonl carries task titles, unlike every other stats artifact.
	PlanLedger bool
	// KBStore selects the optional knowledge-store integration used for
	// output adaptation by the prime/extract tools. Empty string (the
	// default) disables KB-specific output (e.g. paste-ready commands);
	// "basic-memory" enables Basic Memory-shaped output. Any other value
	// is rejected at startup.
	KBStore string
	// Codescene gates the validate_completion CodeScene adoption check.
	// "" (default) disables it entirely — no findings, today's behaviour.
	// "required" makes a missing `codescene` argument an observable
	// non-adoption signal. Operator-declared rather than agent-declared:
	// if absence were interpreted from the agent's own claim about its host,
	// a forgetful agent and an unconfigured host would look identical.
	Codescene string
}

type ModelRef struct {
	Provider string
	Model    string
}

func (m ModelRef) String() string { return m.Provider + ":" + m.Model }

func ParseModelRef(s string) (ModelRef, error) {
	provider, model, ok := strings.Cut(s, ":")
	if !ok || provider == "" || model == "" {
		return ModelRef{}, fmt.Errorf("invalid model ref %q: expected provider:model", s)
	}
	return ModelRef{Provider: provider, Model: model}, nil
}

// Load reads configuration from the given env lookup function.
// Pass os.Getenv in production; pass a map-backed function in tests.
func Load(env func(string) string) (Config, error) {
	cfg := Config{
		AnthropicKey:           env("ANTHROPIC_API_KEY"),
		OpenAIKey:              env("OPENAI_API_KEY"),
		GoogleKey:              env("GOOGLE_API_KEY"),
		SessionTTL:             4 * time.Hour,
		MaxPayloadBytes:        204800,
		RequestTimeout:         180 * time.Second,
		LogLevel:               slog.LevelInfo,
		PerTaskMaxTokens:       4096,
		PlanMaxTokens:          4096,
		PrimeMaxTokens:         4096,
		ExtractMaxTokens:       8192,
		PlanTasksPerChunk:      8,
		MaxTokensCeiling:       16384,
		PlanMaxPayloadBytes:    1048576,
		ContextMaxFileBytes:    131072,
		ContextMaxPayloadBytes: 524288,
		StatsSummaryInterval:   24 * time.Hour,
		StatsSummaryThreshold:  50,
		StatsRetentionDays:     30,
		StatsMaxTokens:         2048,
	}

	if cfg.AnthropicKey == "" && cfg.OpenAIKey == "" && cfg.GoogleKey == "" {
		return Config{}, errors.New("at least one of ANTHROPIC_API_KEY, OPENAI_API_KEY, GOOGLE_API_KEY must be set")
	}

	defaults := map[*ModelRef][2]string{
		&cfg.PreModel:  {"ANTI_TANGENT_PRE_MODEL", "anthropic:claude-sonnet-4-6"},
		&cfg.MidModel:  {"ANTI_TANGENT_MID_MODEL", "anthropic:claude-haiku-4-5-20251001"},
		&cfg.PostModel: {"ANTI_TANGENT_POST_MODEL", "anthropic:claude-opus-4-7"},
	}
	for ptr, spec := range defaults {
		val := env(spec[0])
		if val == "" {
			val = spec[1]
		}
		mr, err := ParseModelRef(val)
		if err != nil {
			return Config{}, fmt.Errorf("%s: %w", spec[0], err)
		}
		*ptr = mr
	}

	// PlanModel: optional override; defaults to whatever PreModel resolved to.
	if v := env("ANTI_TANGENT_PLAN_MODEL"); v != "" {
		mr, err := ParseModelRef(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_PLAN_MODEL: %w", err)
		}
		cfg.PlanModel = mr
	} else {
		cfg.PlanModel = cfg.PreModel
	}

	// PrimeModel / ExtractModel: optional overrides used by the v0.6.0
	// project-knowledge tools. Resolution order is explicit env override
	// -> resolved PlanModel -> resolved PreModel. PlanModel itself already
	// falls back to PreModel above, so assigning PlanModel here gives the
	// full chain.
	if v := env("ANTI_TANGENT_PRIME_MODEL"); v != "" {
		mr, err := ParseModelRef(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_PRIME_MODEL: %w", err)
		}
		cfg.PrimeModel = mr
	} else {
		cfg.PrimeModel = cfg.PlanModel
	}
	if v := env("ANTI_TANGENT_EXTRACT_MODEL"); v != "" {
		mr, err := ParseModelRef(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_EXTRACT_MODEL: %w", err)
		}
		cfg.ExtractModel = mr
	} else {
		cfg.ExtractModel = cfg.PlanModel
	}

	// KBStore: optional knowledge-store selector. Empty (the default)
	// disables KB-specific output adaptation; "basic-memory" enables it.
	// Any other value is a startup error naming the env var.
	if v := env("ANTI_TANGENT_KB_STORE"); v != "" {
		switch v {
		case "basic-memory":
			cfg.KBStore = v
		default:
			return Config{}, fmt.Errorf("ANTI_TANGENT_KB_STORE: unknown value %q (allowed: \"\", \"basic-memory\")", v)
		}
	}

	if v := env("ANTI_TANGENT_CODESCENE"); v != "" {
		if v != "required" {
			return Config{}, fmt.Errorf(`ANTI_TANGENT_CODESCENE: unknown value %q (allowed: "", "required")`, v)
		}
		cfg.Codescene = v
	}

	if v := env("ANTI_TANGENT_SESSION_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_SESSION_TTL: %w", err)
		}
		if d <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_SESSION_TTL: must be positive, got %s", d)
		}
		cfg.SessionTTL = d
	}
	if v := env("ANTI_TANGENT_MAX_PAYLOAD_BYTES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_MAX_PAYLOAD_BYTES: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_MAX_PAYLOAD_BYTES: must be positive, got %d", n)
		}
		cfg.MaxPayloadBytes = n
	}
	// PlanMaxPayloadBytes tracks the shared cap when an operator raises it: a
	// host that already raised ANTI_TANGENT_MAX_PAYLOAD_BYTES above the 1MB
	// plan default must not regress to rejecting plans it accepted before
	// validate_plan gained its own cap. cfg.PlanMaxPayloadBytes is still
	// initialized to 1048576 above, so this is exactly max(1048576,
	// cfg.MaxPayloadBytes). The explicit ANTI_TANGENT_PLAN_MAX_PAYLOAD_BYTES
	// override below always wins over this default, even if lower.
	if cfg.MaxPayloadBytes > cfg.PlanMaxPayloadBytes {
		cfg.PlanMaxPayloadBytes = cfg.MaxPayloadBytes
	}
	if v := env("ANTI_TANGENT_REQUEST_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_REQUEST_TIMEOUT: %w", err)
		}
		if d <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_REQUEST_TIMEOUT: must be positive, got %s", d)
		}
		cfg.RequestTimeout = d
	}
	if v := env("ANTI_TANGENT_PER_TASK_MAX_TOKENS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_PER_TASK_MAX_TOKENS: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_PER_TASK_MAX_TOKENS: must be positive, got %d", n)
		}
		cfg.PerTaskMaxTokens = n
	}
	if v := env("ANTI_TANGENT_PLAN_MAX_TOKENS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_PLAN_MAX_TOKENS: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_PLAN_MAX_TOKENS: must be positive, got %d", n)
		}
		cfg.PlanMaxTokens = n
	}
	if v := env("ANTI_TANGENT_PRIME_MAX_TOKENS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_PRIME_MAX_TOKENS: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_PRIME_MAX_TOKENS: must be positive, got %d", n)
		}
		cfg.PrimeMaxTokens = n
	}
	if v := env("ANTI_TANGENT_EXTRACT_MAX_TOKENS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_EXTRACT_MAX_TOKENS: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_EXTRACT_MAX_TOKENS: must be positive, got %d", n)
		}
		cfg.ExtractMaxTokens = n
	}
	if v := env("ANTI_TANGENT_PLAN_TASKS_PER_CHUNK"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_PLAN_TASKS_PER_CHUNK: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_PLAN_TASKS_PER_CHUNK: must be positive, got %d", n)
		}
		cfg.PlanTasksPerChunk = n
	}
	if v := env("ANTI_TANGENT_PLAN_MAX_PAYLOAD_BYTES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_PLAN_MAX_PAYLOAD_BYTES: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_PLAN_MAX_PAYLOAD_BYTES: must be positive, got %d", n)
		}
		cfg.PlanMaxPayloadBytes = n
	}
	// fileCapExplicit distinguishes "the operator set a per-file cap" from
	// "the default is still in place" — the cross-check below treats the two
	// cases differently, and by then the value alone cannot tell them apart.
	fileCapExplicit := false
	if v := env("ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES: must be positive, got %d", n)
		}
		cfg.ContextMaxFileBytes = n
		fileCapExplicit = true
	}
	if v := env("ANTI_TANGENT_CONTEXT_MAX_PAYLOAD_BYTES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_CONTEXT_MAX_PAYLOAD_BYTES: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_CONTEXT_MAX_PAYLOAD_BYTES: must be positive, got %d", n)
		}
		cfg.ContextMaxPayloadBytes = n
	}
	// Cross-check, after BOTH have been read: a per-file cap above the
	// whole-set cap is unreachable configuration. Every file that passes the
	// per-file check would then trip the set check.
	//
	// Which of the two responses is right depends on whether the operator
	// ASKED for the per-file cap they got:
	//
	//   - Explicitly set above the payload cap → hard error naming both
	//     variables. Raising ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES to "allow a
	//     big file" would otherwise achieve nothing at all, the operator's
	//     intent silently defeated by a limit they did not think to raise.
	//     Failing at startup naming both beats failing at review time naming
	//     one.
	//   - Still the DEFAULT (131072) and merely larger than a payload cap
	//     the operator lowered → clamp it down. Erroring here made
	//     ANTI_TANGENT_CONTEXT_MAX_PAYLOAD_BYTES below 131072 refuse to boot
	//     at all: Load returns an error, main exits 1, and every
	//     anti-tangent tool vanishes from the host — a startup-denial bug
	//     for a value the operator never touched.
	//
	// Deliberately NOT "clamp always, with a warning": Load runs before the
	// JSON logger is installed, so the warning would go nowhere, and an
	// unconditional clamp reintroduces exactly the silent defeat the error
	// exists to stop.
	if cfg.ContextMaxFileBytes > cfg.ContextMaxPayloadBytes {
		if fileCapExplicit {
			return Config{}, fmt.Errorf(
				"ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES (%d) must be <= ANTI_TANGENT_CONTEXT_MAX_PAYLOAD_BYTES (%d): a per-file cap above the whole-set cap can never be reached",
				cfg.ContextMaxFileBytes, cfg.ContextMaxPayloadBytes)
		}
		cfg.ContextMaxFileBytes = cfg.ContextMaxPayloadBytes
	}
	if v := env("ANTI_TANGENT_PLAN_ROOTS"); v != "" {
		for _, p := range filepath.SplitList(v) {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if !filepath.IsAbs(p) {
				return Config{}, fmt.Errorf("ANTI_TANGENT_PLAN_ROOTS: %q is not an absolute path", p)
			}
			cfg.PlanRoots = append(cfg.PlanRoots, resolvePlanRoot(p))
		}
		// ANTI_TANGENT_PLAN_ROOTS is a security-narrowing setting: nil
		// PlanRoots means "no restriction" (see withinRoots), so a value
		// that is set but yields zero usable entries (e.g. ":" alone, or a
		// string that TrimSpaces to nothing per entry) would silently leave
		// file access wide open instead of narrowed — fail closed instead.
		if len(cfg.PlanRoots) == 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_PLAN_ROOTS: set but contains no usable path entries: %q", v)
		}
	}
	if v := env("ANTI_TANGENT_MAX_TOKENS_CEILING"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_MAX_TOKENS_CEILING: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_MAX_TOKENS_CEILING: must be positive, got %d", n)
		}
		cfg.MaxTokensCeiling = n
	}

	cfg.StatsDir = env("ANTI_TANGENT_STATS_DIR")

	// StatsModel: explicit override -> MidModel.
	if v := env("ANTI_TANGENT_STATS_MODEL"); v != "" {
		mr, err := ParseModelRef(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_STATS_MODEL: %w", err)
		}
		cfg.StatsModel = mr
	} else {
		cfg.StatsModel = cfg.MidModel
	}

	if v := env("ANTI_TANGENT_STATS_SUMMARY_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_STATS_SUMMARY_INTERVAL: %w", err)
		}
		if d <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_STATS_SUMMARY_INTERVAL: must be positive, got %s", d)
		}
		cfg.StatsSummaryInterval = d
	}
	if v := env("ANTI_TANGENT_STATS_SUMMARY_THRESHOLD"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_STATS_SUMMARY_THRESHOLD: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_STATS_SUMMARY_THRESHOLD: must be positive, got %d", n)
		}
		cfg.StatsSummaryThreshold = n
	}
	if v := env("ANTI_TANGENT_STATS_RETENTION_DAYS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_STATS_RETENTION_DAYS: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_STATS_RETENTION_DAYS: must be positive, got %d", n)
		}
		cfg.StatsRetentionDays = n
	}
	if v := env("ANTI_TANGENT_STATS_MAX_TOKENS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_STATS_MAX_TOKENS: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_STATS_MAX_TOKENS: must be positive, got %d", n)
		}
		cfg.StatsMaxTokens = n
	}
	if cfg.StatsMaxTokens > cfg.MaxTokensCeiling {
		cfg.StatsMaxTokens = cfg.MaxTokensCeiling
	}

	if v := env("ANTI_TANGENT_PLAN_LEDGER"); v == "1" || strings.EqualFold(v, "true") {
		cfg.PlanLedger = true
	}

	if v := env("ANTI_TANGENT_LOG_LEVEL"); v != "" {
		switch strings.ToLower(v) {
		case "debug":
			cfg.LogLevel = slog.LevelDebug
		case "info":
			cfg.LogLevel = slog.LevelInfo
		case "warn":
			cfg.LogLevel = slog.LevelWarn
		case "error":
			cfg.LogLevel = slog.LevelError
		default:
			return Config{}, fmt.Errorf("ANTI_TANGENT_LOG_LEVEL: unknown level %q", v)
		}
	}

	return cfg, nil
}

// resolvePlanRoot symlink-resolves an ANTI_TANGENT_PLAN_ROOTS entry so it
// compares like-for-like against the candidate paths withinRoots checks it
// against: resolveFileInput (internal/mcpsrv/file_source.go) EvalSymlinks the
// candidate path BEFORE checking root membership, so a root left merely
// Cleaned — rather than resolved — refuses every legitimate path under it
// whenever an ANCESTOR of the root is itself a symlink (e.g. macOS's
// /tmp -> /private/tmp). Falls back to the Cleaned value when the root does
// not exist yet at server-start time: a root created later must not be a
// startup error.
func resolvePlanRoot(root string) string {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return filepath.Clean(root)
	}
	return resolved
}
