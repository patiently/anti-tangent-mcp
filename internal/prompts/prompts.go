// Package prompts renders hook-specific prompts for the reviewer LLM.
package prompts

import (
	"bytes"
	"embed"
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

// planSuffixIndex returns the byte offset of planSuffixMarker where it
// begins a LINE, not any mid-line occurrence. PlanText — arbitrary
// user-supplied markdown — is interpolated into the shared region before
// the real "## What to evaluate" heading, so an unanchored substring search
// would match a plan that merely discusses that heading text inside a
// paragraph and silently shrink the cacheable prefix. Returns -1 if the
// marker never starts a line.
func planSuffixIndex(body string) int {
	if strings.HasPrefix(body, planSuffixMarker) {
		return 0
	}
	if i := strings.Index(body, "\n"+planSuffixMarker); i >= 0 {
		return i + 1 // skip the newline itself; suffix starts at "##..."
	}
	return -1
}

type File struct {
	Path    string
	Content string
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

func RenderPlan(in PlanInput) (Output, error) {
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
}

// RenderPlanTasksChunk produces a per-chunk prompt for the chunked validate_plan
// path: full plan as context, but the reviewer is instructed to emit results
// only for the subset of tasks in ChunkTasks.
func RenderPlanTasksChunk(in PlanChunkInput) (Output, error) {
	body, err := render("plan_tasks_chunk.tmpl", in)
	if err != nil {
		return Output{}, err
	}
	return splitPlanPrompt(body), nil
}

// RenderPlanFindingsOnly produces the Pass-1 prompt for the chunked validate_plan
// path: full plan as context, plan-level findings only, no per-task data.
func RenderPlanFindingsOnly(in PlanInput) (Output, error) {
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

func render(name string, data any) (string, error) {
	tmpl, err := template.New("").ParseFS(templatesFS, "templates/"+name)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return "", fmt.Errorf("execute %s: %w", name, err)
	}
	return buf.String(), nil
}
