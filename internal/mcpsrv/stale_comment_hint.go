package mcpsrv

import (
	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/prompts"
	"github.com/patiently/anti-tangent-mcp/internal/stalecomments"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// staleCommentHint builds validate_completion's lead for the stale-comment
// check: the names the diff removes, and the comment lines of the post-change
// files that still contain one. Only those lines reach the prompt, so reading
// whole files under repo_root does not grow the payload. The hint is nil when
// no comment line matches. advisories holds the one repo_root advisory when
// repo_root was supplied and cannot be used, and is nil otherwise — the
// caller appends it unconditionally, with no nil branch of its own.
func staleCommentHint(cfg config.Config, finalDiff, repoRoot string, files []FileArg) (*prompts.StaleCommentHint, []verdict.Finding) {
	root, advisories := resolveRepoRootForHint(cfg, repoRoot)
	diffFiles := stalecomments.ParseDiff(finalDiff)
	names := stalecomments.RemovedNames(diffFiles)
	if len(names) == 0 {
		return nil, advisories
	}
	hits := stalecomments.Scan(names, postChangeSources(cfg, diffFiles, files, root))
	if len(hits) == 0 {
		return nil, advisories
	}
	return hintFromHits(hits), advisories
}

// resolveRepoRootForHint resolves repo_root for staleCommentHint: empty input
// resolves to no root and no advisories, and a root resolveDirInput rejects
// resolves to no root plus one repo_root advisory.
func resolveRepoRootForHint(cfg config.Config, repoRoot string) (string, []verdict.Finding) {
	if repoRoot == "" {
		return "", nil
	}
	resolved, err := resolveDirInput(repoRoot, cfg.PlanRoots)
	if err != nil {
		return "", []verdict.Finding{repoRootUnusableAdvisory(err.Error())}
	}
	return resolved, nil
}

// hintFromHits builds the StaleCommentHint from Scan's hits: the names it
// matched, deduplicated in order of first appearance, and every hit line.
func hintFromHits(hits []stalecomments.Hit) *prompts.StaleCommentHint {
	hint := &prompts.StaleCommentHint{}
	named := map[string]bool{}
	for _, hit := range hits {
		if !named[hit.Name] {
			named[hit.Name] = true
			hint.Names = append(hint.Names, hit.Name)
		}
		hint.Hits = append(hint.Hits, hit.String())
	}
	return hint
}

// postChangeSources lists the post-change content to scan: one source per
// file the diff names, then one per final_files entry the diff does not name.
// A diff file is read under root when root is set and the read succeeds;
// otherwise its final_files entry stands in for it, and failing that the
// diff's own context and added lines do. A deleted file contributes nothing.
func postChangeSources(cfg config.Config, diffFiles []stalecomments.File, files []FileArg, root string) []stalecomments.Source {
	covered := make([]bool, len(files))
	ctx := &diskReadContext{
		cfg:    cfg,
		root:   root,
		budget: &diskReadBudget{files: maxContextFiles, bytes: cfg.ContextMaxPayloadBytes},
	}
	var sources []stalecomments.Source
	for _, df := range diffFiles {
		if df.Path == "" {
			continue
		}
		entry := markCoveredFinalFiles(files, df.Path, covered)
		sources = append(sources, diffFileSource(ctx, df, files, entry))
	}
	return append(sources, uncoveredFinalFileSources(files, covered)...)
}

// diskReadContext groups the inputs postChangeSources holds constant across
// every diff file in its loop — the config, the resolved repo_root, and the
// shared read budget — so diffFileSource takes one argument for them instead
// of three.
type diskReadContext struct {
	cfg    config.Config
	root   string
	budget *diskReadBudget
}

// markCoveredFinalFiles marks every final_files entry that names the same
// file as diffPath and returns the index of the first one, or -1 when none
// matches.
func markCoveredFinalFiles(files []FileArg, diffPath string, covered []bool) int {
	entry := -1
	for i, f := range files {
		if !pathTailMatches(f.Path, diffPath) {
			continue
		}
		covered[i] = true
		if entry < 0 {
			entry = i
		}
	}
	return entry
}

// diffFileSource picks the post-change content for one diff file: the disk
// read under ctx.root when it succeeds, else the final_files entry at entry
// (-1 for none), else the diff's own context and added lines.
func diffFileSource(ctx *diskReadContext, df stalecomments.File, files []FileArg, entry int) stalecomments.Source {
	lines := df.Post
	if content, ok := readUnderRepoRoot(ctx.cfg, ctx.root, df.Path, ctx.budget); ok {
		lines = stalecomments.FileLines(content)
	} else if entry >= 0 {
		lines = stalecomments.FileLines(files[entry].Content)
	}
	return stalecomments.Source{Path: df.Path, Lines: lines}
}

// uncoveredFinalFileSources lists the final_files entries no diff file names.
func uncoveredFinalFileSources(files []FileArg, covered []bool) []stalecomments.Source {
	var sources []stalecomments.Source
	for i, f := range files {
		if !covered[i] {
			sources = append(sources, stalecomments.Source{Path: f.Path, Lines: stalecomments.FileLines(f.Content)})
		}
	}
	return sources
}

// diskReadBudget is what remains of the context-file caps for one call's reads
// under repo_root: attempts, so a diff naming thousands of missing files costs
// a bounded number of lookups, and bytes.
type diskReadBudget struct {
	files int
	bytes int
}

// readUnderRepoRoot reads rel beneath root under the rules a context_paths
// attachment follows: symlinks are resolved (including at rel's own last
// component), and the resolved path must stay beneath both
// ANTI_TANGENT_PLAN_ROOTS and root, so a symlink in the checkout cannot pull
// in a file from elsewhere. The open that follows then refuses only a
// symlink introduced at the last component of that already-resolved path —
// the race window between the roots check and the read — and requires a
// regular file within the per-file byte cap. It reports false, having spent
// one attempt, when the read fails or would overrun the byte budget.
func readUnderRepoRoot(cfg config.Config, root, rel string, budget *diskReadBudget) (string, bool) {
	if root == "" || budget.files <= 0 {
		return "", false
	}
	budget.files--
	abs, ok := resolveUnderRoot(root, rel)
	if !ok {
		return "", false
	}
	content, src, err := resolveFileInput(abs, cfg.PlanRoots, cfg.ContextMaxFileBytes)
	if err != nil {
		return "", false
	}
	if !withinRoots(src.Path, []string{root}) {
		return "", false
	}
	if src.Bytes > budget.bytes {
		return "", false
	}
	budget.bytes -= src.Bytes
	return content, true
}

// repoRootUnusableAdvisory reports a validate_completion repo_root the server
// could not use. It is appended after the verdict is finalized.
func repoRootUnusableAdvisory(reason string) verdict.Finding {
	return ignoredArgumentAdvisory("repo_root",
		"repo_root unusable ("+reason+"); comments were looked for in the submitted evidence only.",
		"Pass repo_root as the absolute path of the checkout the diff applies to, inside ANTI_TANGENT_PLAN_ROOTS when that is set, or omit it.")
}
