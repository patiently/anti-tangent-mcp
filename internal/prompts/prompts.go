// Package prompts renders hook-specific prompts for the reviewer LLM.
package prompts

import (
	"bytes"
	"embed"
	"fmt"
	"text/template"

	"github.com/patiently/anti-tangent-mcp/internal/planparser"
	"github.com/patiently/anti-tangent-mcp/internal/session"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

//go:embed templates/*.tmpl
var templatesFS embed.FS

type Output struct {
	System string
	User   string
}

type File struct {
	Path    string
	Content string
}

type PreInput struct {
	Spec session.TaskSpec
}

type MidInput struct {
	Spec          session.TaskSpec
	PriorFindings []verdict.Finding
	WorkingOn     string
	Files         []File
	Questions     []string
}

type PostInput struct {
	Spec         session.TaskSpec
	Summary      string
	Files        []File
	TestEvidence string
}

type PlanInput struct {
	PlanText string
}

const systemPrompt = `You are an exacting reviewer. You return ONLY a JSON object matching the provided schema. You give specific, evidence-backed findings. You never invent facts about code that wasn't shown to you.`

func RenderPre(in PreInput) (Output, error) {
	body, err := render("pre.tmpl", in)
	if err != nil {
		return Output{}, err
	}
	return Output{System: systemPrompt, User: body}, nil
}

func RenderMid(in MidInput) (Output, error) {
	body, err := render("mid.tmpl", in)
	if err != nil {
		return Output{}, err
	}
	return Output{System: systemPrompt, User: body}, nil
}

func RenderPost(in PostInput) (Output, error) {
	body, err := render("post.tmpl", in)
	if err != nil {
		return Output{}, err
	}
	return Output{System: systemPrompt, User: body}, nil
}

func RenderPlan(in PlanInput) (Output, error) {
	body, err := render("plan.tmpl", in)
	if err != nil {
		return Output{}, err
	}
	return Output{System: systemPrompt, User: body}, nil
}

// PlanChunkInput is the input for one per-task chunk in chunked validate_plan.
// ChunkTasks carries the exact subset of tasks the reviewer should emit
// results for; PlanText carries the full plan for cross-task reasoning.
type PlanChunkInput struct {
	PlanText   string
	ChunkTasks []planparser.RawTask
}

// RenderPlanTasksChunk produces a per-chunk prompt for the chunked validate_plan
// path: full plan as context, but the reviewer is instructed to emit results
// only for the subset of tasks in ChunkTasks.
func RenderPlanTasksChunk(in PlanChunkInput) (Output, error) {
	body, err := render("plan_tasks_chunk.tmpl", in)
	if err != nil {
		return Output{}, err
	}
	return Output{System: systemPrompt, User: body}, nil
}

// RenderPlanFindingsOnly produces the Pass-1 prompt for the chunked validate_plan
// path: full plan as context, plan-level findings only, no per-task data.
func RenderPlanFindingsOnly(in PlanInput) (Output, error) {
	body, err := render("plan_findings_only.tmpl", in)
	if err != nil {
		return Output{}, err
	}
	return Output{System: systemPrompt, User: body}, nil
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
