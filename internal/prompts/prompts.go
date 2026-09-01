// Package prompts renders hook-specific prompts for the reviewer LLM.
package prompts

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"fmt"
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
	// terminator. Left empty, RenderPlan/RenderPlanFindingsOnly generate a
	// fresh random one; callers that need several render calls to share a
	// byte-identical cacheable prefix (the chunked validate_plan path) or
	// tests that need a stable golden must set it explicitly. See
	// NewContextFilesNonce.
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

// NewContextFilesNonce returns a fresh random hex token (crypto/rand-backed,
// so it cannot be predicted from an attached file's own content ahead of
// render time) for pairing the BEGIN/END FILE delimiters around attached
// context files. RenderPlan, RenderPlanFindingsOnly, and RenderPlanTasksChunk
// generate one automatically whenever ContextFiles is non-empty and
// ContextFilesNonce is left unset. It is exported so a caller that issues
// several render calls sharing one set of ContextFiles — the chunked
// validate_plan path in mcpsrv — can generate ONE nonce up front and pass it
// into every call, keeping their UserPrefix byte-identical.
func NewContextFilesNonce() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing is effectively unreachable on supported
		// platforms; a fixed fallback keeps rendering available rather than
		// failing a review over delimiter cosmetics.
		return "00000000"
	}
	return hex.EncodeToString(b)
}

func ensureContextFilesNonce(in PlanInput) PlanInput {
	if len(in.ContextFiles) > 0 && in.ContextFilesNonce == "" {
		in.ContextFilesNonce = NewContextFilesNonce()
	}
	return in
}

func ensureContextFilesNonceChunk(in PlanChunkInput) PlanChunkInput {
	if len(in.ContextFiles) > 0 && in.ContextFilesNonce == "" {
		in.ContextFilesNonce = NewContextFilesNonce()
	}
	return in
}

func RenderPlan(in PlanInput) (Output, error) {
	in = ensureContextFilesNonce(in)
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
	// of these per plan review, all sharing the same ContextFiles — callers
	// MUST pass the same nonce to every one of those calls, or their
	// UserPrefix stops being byte-identical and the provider-side prompt
	// cache silently stops matching.
	ContextFilesNonce string
}

// RenderPlanTasksChunk produces a per-chunk prompt for the chunked validate_plan
// path: full plan as context, but the reviewer is instructed to emit results
// only for the subset of tasks in ChunkTasks.
func RenderPlanTasksChunk(in PlanChunkInput) (Output, error) {
	in = ensureContextFilesNonceChunk(in)
	body, err := render("plan_tasks_chunk.tmpl", in)
	if err != nil {
		return Output{}, err
	}
	return splitPlanPrompt(body), nil
}

// RenderPlanFindingsOnly produces the Pass-1 prompt for the chunked validate_plan
// path: full plan as context, plan-level findings only, no per-task data.
func RenderPlanFindingsOnly(in PlanInput) (Output, error) {
	in = ensureContextFilesNonce(in)
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
