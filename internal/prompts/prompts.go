// Package prompts renders hook-specific prompts for the reviewer LLM.
package prompts

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"text/template"

	"github.com/patiently/anti-tangent-mcp/internal/codescene"
	"github.com/patiently/anti-tangent-mcp/internal/planparser"
	"github.com/patiently/anti-tangent-mcp/internal/session"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

//go:embed templates/*.tmpl
var templatesFS embed.FS

type Output struct {
	System string
	// User is the full user prompt — always UserPrefix + UserSuffix.
	User string
	// UserPrefix is the cacheable shared head (reviewer ground rules, project
	// knowledge, and the plan itself), populated only for the chunked
	// validate_plan templates where several calls share it byte-for-byte.
	// Empty everywhere else, which is what keeps single-call renders from
	// paying a cache-write premium against zero reads.
	UserPrefix string
	// UserSuffix is the per-call remainder. Equal to User when UserPrefix is
	// empty.
	UserSuffix string
}

// planSuffixMarker is the first line of the per-call section in both chunked
// plan templates. Everything before it — ground rules, project knowledge, and
// the plan under review — is byte-identical across the findings-only call and
// every chunk call, which is exactly the prefix worth caching.
const planSuffixMarker = "## What to evaluate"

// splitPlanPrompt divides a rendered chunked-plan prompt at planSuffixMarker.
// If the marker is absent the whole body becomes the suffix, so a template
// edit that removes it degrades to today's uncached behavior rather than
// silently caching the wrong span.
func splitPlanPrompt(body string) Output {
	idx := planSuffixIndex(body)
	if idx < 0 {
		return Output{System: systemPrompt, User: body, UserSuffix: body}
	}
	return Output{
		System:     systemPrompt,
		User:       body,
		UserPrefix: body[:idx],
		UserSuffix: body[idx:],
	}
}

// planSuffixIndex returns the byte offset of the LAST occurrence of
// planSuffixMarker that begins a LINE, not any mid-line occurrence. PlanText
// — arbitrary user-supplied markdown — is interpolated into the shared
// region before the real "## What to evaluate" heading, so an unanchored
// substring search would match a plan that merely discusses that heading
// text inside a paragraph and silently shrink the cacheable prefix.
// Line-anchoring alone only rejects a MID-LINE hit, though: a plan that
// itself contains a "## What to evaluate" heading at the START of a line
// (plausible for a plan about prompt templates, this repo's own included)
// would still split at that embedded heading instead of the real one.
// Both chunked plan templates (plan_findings_only.tmpl, plan_tasks_chunk.tmpl)
// contain exactly one occurrence of the marker, and the interpolated plan
// text always precedes it, so the real heading is always the LAST line-start
// occurrence in the body — hence LastIndex here, not Index. Returns -1 if
// the marker never starts a line.
func planSuffixIndex(body string) int {
	if i := strings.LastIndex(body, "\n"+planSuffixMarker); i >= 0 {
		return i + 1 // skip the newline itself; suffix starts at "##..."
	}
	if strings.HasPrefix(body, planSuffixMarker) {
		return 0
	}
	return -1
}

type File struct {
	Path    string
	Content string
}

// ContextFile is one source file attached to validate_plan via context_paths.
// Path is the symlink-resolved path actually read, and SHA256Short is the
// first 8 hex digits — the same provenance the summary block shows — so the
// reviewer can cite a file by the identity the human will see. Content is the
// COMPLETE file: the ground rules promise the reviewer that absence within an
// attached file is evidence, which is only true if nothing was truncated.
type ContextFile struct {
	Path        string
	Bytes       int
	SHA256Short string
	Content     string
}

type PreInput struct {
	Spec             session.TaskSpec
	ProjectKnowledge string
}

type MidInput struct {
	Spec          session.TaskSpec
	PriorFindings []verdict.Finding
	WorkingOn     string
	Files         []File
	Questions     []string
}

type PostInput struct {
	Spec                           session.TaskSpec
	Summary                        string
	Files                          []File
	FinalDiff                      string
	TestEvidence                   string
	MajorPreFindings               []verdict.Finding
	ReferencedPathsMissingEvidence []string
	ExitContracts                  []string
	ExitContractsInferred          bool
	Codescene                      *codescene.Digest
}

type PlanInput struct {
	PlanText         string
	ProjectKnowledge string
	Mode             string
	ContextFiles     []ContextFile
	// ContextFilesNonce pairs the BEGIN/END FILE delimiters around each
	// attached file so a decoy delimiter-shaped line inside attached content
	// (this repo's own templates, golden files, and docs all contain literal
	// "--- END FILE: ... ---" lines) cannot be mistaken for the real
	// terminator. Left empty, RenderPlan/RenderPlanFindingsOnly derive one
	// DETERMINISTICALLY from ContextFiles (see DeriveContextFilesNonce), so
	// two renders of the SAME attachment set produce byte-identical
	// output — required for mcpsrv's plan-pass cache to ever hit on a
	// context_paths call, the calls that cost the most. An explicitly-set
	// value always wins over the derived one, which is how tests pin a
	// stable golden or exercise a nonce that deliberately does not match
	// content (see NewContextFilesNonce).
	ContextFilesNonce string
}

type KBIndexEntry struct {
	Permalink string
	Type      string
	Title     string
	Summary   string
	Tags      []string
}

type PrimeInput struct {
	TaskTitle            string
	Goal                 string
	AcceptanceCriteria   []string
	NonGoals             []string
	Context              string
	KBIndex              []KBIndexEntry
	EpicPermalink        string
	MaxPicks             int
	KBStoreIsBasicMemory bool
}

// CompletionEnvelopeForExtract is one completion-stage envelope the caller
// has accumulated for extract review. The reviewer treats every field as
// caller-attested evidence; evidence_refs on emitted Proposals must point
// at fields visible in one of these envelopes (or at the optional PlanText).
type CompletionEnvelopeForExtract struct {
	TaskTitle    string
	Summary      string
	Verdict      string
	Findings     []verdict.Finding
	FinalDiff    string
	FinalFiles   []File
	TestEvidence string
}

// ExtractInput is the rendering input for extract.tmpl. The reviewer is
// asked to propose KB writes (decisions/modules/features/glossary/epic
// ledger entries) grounded in CompletionEnvelopes and PlanText.
type ExtractInput struct {
	CompletionEnvelopes  []CompletionEnvelopeForExtract
	PlanText             string
	KBIndex              []KBIndexEntry
	CurrentKBExcerpts    map[string]string
	EpicPermalink        string
	KBStoreIsBasicMemory bool
}

const systemPrompt = `You are an exacting reviewer. You return ONLY a JSON object matching the provided schema. You give specific, evidence-backed findings. You never invent facts about code that wasn't shown to you.`

func RenderPre(in PreInput) (Output, error) {
	body, err := render("pre.tmpl", in)
	if err != nil {
		return Output{}, err
	}
	return Output{System: systemPrompt, User: body, UserSuffix: body}, nil
}

func RenderMid(in MidInput) (Output, error) {
	body, err := render("mid.tmpl", in)
	if err != nil {
		return Output{}, err
	}
	return Output{System: systemPrompt, User: body, UserSuffix: body}, nil
}

func RenderPost(in PostInput) (Output, error) {
	body, err := render("post.tmpl", in)
	if err != nil {
		return Output{}, err
	}
	return Output{System: systemPrompt, User: body, UserSuffix: body}, nil
}

// NewContextFilesNonce returns a fresh random hex token (crypto/rand-backed).
// The production render path does NOT call this: RenderPlan,
// RenderPlanFindingsOnly, and RenderPlanTasksChunk derive a DETERMINISTIC
// nonce from ContextFiles via DeriveContextFilesNonce whenever
// ContextFilesNonce is left unset, so that two separate renders of the same
// attachment set produce byte-identical prompts (mcpsrv's plan-pass cache
// hashes the rendered prompt text; a random per-render nonce would make
// every validate_plan call carrying context_paths a permanent cache miss —
// exactly the calls that cost the most). NewContextFilesNonce stays exported
// for tests that want an explicit override which deliberately does NOT
// match the attached content, e.g. to prove an explicitly-set
// ContextFilesNonce always wins over the derived value.
func NewContextFilesNonce() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing is effectively unreachable on supported
		// platforms; a fixed fallback keeps this usable rather than panicking.
		return "00000000"
	}
	return hex.EncodeToString(b)
}

// contextNonceHexLen is the length, in hex characters, of a context-files
// nonce — whether derived (DeriveContextFilesNonce) or random
// (NewContextFilesNonce).
const contextNonceHexLen = 8

// contextNonceMaxAttempts bounds DeriveContextFilesNonce's belt-and-braces
// collision retry loop. Reachable only if a derived token happens to appear,
// verbatim, as a delimiter-shaped line inside the very content it was
// derived from — a hash-preimage problem, not something constructible by
// accident (or, for an attacker, without already knowing the hash they are
// trying to produce). The cap exists so the loop cannot spin forever;
// hitting it is a bug report, not a plan-authoring mistake.
const contextNonceMaxAttempts = 1000

// contextNonceDelimiterCollides reports whether token appears, at the start
// of a line, as either half of the BEGIN/END FILE delimiter shape inside any
// attached file's content — the situation DeriveContextFilesNonce's retry
// loop exists to catch.
//
// The pattern is deliberately LOOSER than the shape context_files.tmpl
// renders. What matters is not whether a line is byte-identical to a real
// delimiter, but whether a model reading the prompt could take it for one —
// and `----BEGIN FILE <tok>:`, a leading-indented `  --- BEGIN FILE <tok>:`,
// or a tab in place of the colon all read as delimiters while failing an
// exact-shape test. The ground rules tell the reviewer that a marker-shaped
// line carrying the WRONG token is content; they say nothing that would
// help against a near-shape carrying the RIGHT one, which is precisely the
// case this detector has to cover.
//
// Widening costs almost nothing. A false positive here re-derives the nonce
// with the attempt counter folded into the hash and tries again — still
// deterministic, so the plan-pass cache key is unaffected — and the retry
// loop is bounded by contextNonceMaxAttempts.
//
// The token itself is still matched verbatim (QuoteMeta), so a line carrying
// any other token is not a collision at any spelling.
func contextNonceDelimiterCollides(files []ContextFile, token string) bool {
	re := regexp.MustCompile(`(?m)^[ \t]*-{3,}[ \t]*(?:BEGIN|END)[ \t]+FILE[ \t]+` + regexp.QuoteMeta(token) + `[ \t]*[:\t]`)
	for _, f := range files {
		if re.MatchString(f.Content) {
			return true
		}
	}
	return false
}

// DeriveContextFilesNonce computes a DETERMINISTIC nonce from the attached
// files' identity and content, so that two separate renders of the SAME
// attachment set produce a byte-identical prompt:
//
//	nonce = first contextNonceHexLen hex chars of
//	        sha256(for each file, in order: Path || 0x00 || Content || 0x00)
//
// Determinism matters because mcpsrv's plan-pass cache hashes the fully
// rendered prompt text as its cache key; a nonce that varied per render
// would make every validate_plan call carrying context_paths a permanent
// cache miss even when the caller repeats an identical call inside the
// cache TTL.
//
// Uncollidability is preserved despite determinism: the token is a hash of
// the very content it delimits, so content cannot contain a matching
// delimiter line except via a hash preimage — not constructible by
// accident, and not by an attacker either, since producing a specific token
// requires already knowing the hash of the content they are trying to
// smuggle it into.
//
// Belt-and-braces: after deriving, the content is scanned
// (contextNonceDelimiterCollides) for the token as a delimiter-shaped line.
// On a hit — which should be unreachable — the hash input is re-derived with
// a retry counter folded in and tried again, up to contextNonceMaxAttempts
// times, after which an error is returned rather than looping forever.
func DeriveContextFilesNonce(files []ContextFile) (string, error) {
	for attempt := 0; attempt < contextNonceMaxAttempts; attempt++ {
		h := sha256.New()
		for _, f := range files {
			h.Write([]byte(f.Path))
			h.Write([]byte{0})
			h.Write([]byte(f.Content))
			h.Write([]byte{0})
		}
		if attempt > 0 {
			h.Write([]byte(strconv.Itoa(attempt)))
		}
		token := hex.EncodeToString(h.Sum(nil))[:contextNonceHexLen]
		if !contextNonceDelimiterCollides(files, token) {
			return token, nil
		}
	}
	return "", fmt.Errorf(
		"prompts: could not derive a collision-free context files nonce after %d attempts", contextNonceMaxAttempts)
}

func ensureContextFilesNonce(in PlanInput) (PlanInput, error) {
	if len(in.ContextFiles) > 0 && in.ContextFilesNonce == "" {
		nonce, err := DeriveContextFilesNonce(in.ContextFiles)
		if err != nil {
			return in, err
		}
		in.ContextFilesNonce = nonce
	}
	return in, nil
}

func ensureContextFilesNonceChunk(in PlanChunkInput) (PlanChunkInput, error) {
	if len(in.ContextFiles) > 0 && in.ContextFilesNonce == "" {
		nonce, err := DeriveContextFilesNonce(in.ContextFiles)
		if err != nil {
			return in, err
		}
		in.ContextFilesNonce = nonce
	}
	return in, nil
}

func RenderPlan(in PlanInput) (Output, error) {
	in, err := ensureContextFilesNonce(in)
	if err != nil {
		return Output{}, err
	}
	body, err := render("plan.tmpl", in)
	if err != nil {
		return Output{}, err
	}
	return Output{System: systemPrompt, User: body, UserSuffix: body}, nil
}

// PlanChunkInput is the input for one per-task chunk in chunked validate_plan.
// ChunkTasks carries the exact subset of tasks the reviewer should emit
// results for; PlanText carries the full plan for cross-task reasoning.
type PlanChunkInput struct {
	PlanText         string
	ProjectKnowledge string
	ChunkTasks       []planparser.RawTask
	Mode             string
	ContextFiles     []ContextFile
	// ContextFilesNonce: see PlanInput.ContextFilesNonce. The chunked
	// validate_plan path renders one PlanInput (findings-only) plus several
	// of these per plan review, all sharing the same ContextFiles. Because
	// the nonce is derived deterministically from ContextFiles, every one of
	// those calls arrives at the same value on its own — but mcpsrv still
	// derives it once up front and passes it explicitly, to avoid repeating
	// the derivation per chunk and to keep the byte-identical-UserPrefix
	// invariant obvious rather than incidental.
	ContextFilesNonce string
}

// RenderPlanTasksChunk produces a per-chunk prompt for the chunked validate_plan
// path: full plan as context, but the reviewer is instructed to emit results
// only for the subset of tasks in ChunkTasks.
func RenderPlanTasksChunk(in PlanChunkInput) (Output, error) {
	in, err := ensureContextFilesNonceChunk(in)
	if err != nil {
		return Output{}, err
	}
	body, err := render("plan_tasks_chunk.tmpl", in)
	if err != nil {
		return Output{}, err
	}
	return splitPlanPrompt(body), nil
}

// RenderPlanFindingsOnly produces the Pass-1 prompt for the chunked validate_plan
// path: full plan as context, plan-level findings only, no per-task data.
func RenderPlanFindingsOnly(in PlanInput) (Output, error) {
	in, err := ensureContextFilesNonce(in)
	if err != nil {
		return Output{}, err
	}
	body, err := render("plan_findings_only.tmpl", in)
	if err != nil {
		return Output{}, err
	}
	return splitPlanPrompt(body), nil
}

func RenderPrime(in PrimeInput) (Output, error) {
	body, err := render("prime.tmpl", in)
	if err != nil {
		return Output{}, err
	}
	return Output{System: systemPrompt, User: body, UserSuffix: body}, nil
}

func RenderExtract(in ExtractInput) (Output, error) {
	body, err := render("extract.tmpl", in)
	if err != nil {
		return Output{}, err
	}
	return Output{System: systemPrompt, User: body, UserSuffix: body}, nil
}

// render parses the whole embedded template set and executes the named one.
//
// The full set (rather than just the named file) is parsed because the plan
// templates share the "plan_rules" partial — the reviewer ground rules were
// previously duplicated verbatim across all three, which is how a posture
// edit drifts. Parsing eight small templates costs microseconds and always
// precedes an HTTP call to a reviewer LLM, so the waste is unmeasurable next
// to what it prevents.
func render(name string, data any) (string, error) {
	tmpl, err := template.ParseFS(templatesFS, "templates/*.tmpl")
	if err != nil {
		return "", fmt.Errorf("parse templates: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return "", fmt.Errorf("execute %s: %w", name, err)
	}
	return buf.String(), nil
}
