# Audit MEDIUM fixes (v0.29.0) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the three MEDIUM audit findings for v0.29.0: a secret gate on every file the server reads (#105), lightweight `controller_rulings` bound to IDs issued on the plan-run row (#106), and lightweight calls kept off session-attached plan-run rows (#107).

**Architecture:** A new stdlib-only leaf package, `internal/secretgate`, decides whether a path or a byte slice looks like a secret. `config.Load` builds one `*secretgate.Gate`, and `resolveFileInput`, the single reader behind every path input, calls it. `planrun.TaskRow` gains an in-memory `IssuedIDs` list, so a lightweight task's row stands in for the session it lacks. `UpsertLite` refuses session-attached rows, and `Attach` clears the lite results on a row it takes over.

**Tech Stack:** Go 1.25, stdlib (`regexp`, `path`, `path/filepath`), testify, `go test -race`.

**Spec:** `docs/superpowers/specs/2026-10-11-audit-medium-fixes-design.md`

## Global Constraints

- **Do not gate this plan with anti-tangent.** It changes the review behaviour, the evidence reading and the plan-run bookkeeping of the tool itself. Implementers make no `validate_task_spec` / `check_progress` / `validate_completion` calls; the SDD per-task review and the final whole-branch review are the gate. Every dispatch brief still runs CodeScene: `pre_commit_code_health_safeguard` before each commit, and `analyze_change_set` over the task's range before closing it.
- **No literal key-shaped string anywhere in the repo**: no source, test, fixture or doc may contain one. Build content-tier samples at runtime by concatenation (`"AKIA" + strings.Repeat("Q", 16)`, the PEM armour line from parts). Task 1 adds a test that fails if any `internal/secretgate` source file trips the content tier.
- **Comments** follow CLAUDE.md: they explain behaviour or an invariant, and never carry change history (no issue numbers, versions, "previously", "no longer").
- **Error text never quotes matched secret bytes.** A `*secretgate.Refusal` names the path (`%q`), the tier and the rule, and nothing else.
- **Gated file = transport error.** No reviewer call, and on `validate_task_spec` no session.
- Env var names, exactly: `ANTI_TANGENT_SECRET_GATE` (`on` | `names` | `off`, default `on`), `ANTI_TANGENT_FILE_DENY_EXTRA` (OS path-list separated `filepath.Match` globs).
- `IssuedIDs` is capped at **200**, oldest evicted first, never serialized (`json:"-"`).
- `docs/protocol/implementer.md` must stay **< 16,000 bytes**, and `plugin/anti-tangent-protocol/protocol/` must be byte-identical to `docs/protocol/`.
- `CHANGELOG.md` gets a `## [0.29.0] - 2026-10-11` section; the first task that touches it creates it. Do not edit `VERSION`.
- Mainline check after every task: `go build ./... && go vet ./... && go test -race ./...` all pass.

**User decisions (already made):**
- F1: "Path denylist" + "Content secret scan", both on by default. No per-tool opt-out.
- F3: "Bind to plan-run row": a lightweight ruling applies only to an ID an earlier lightweight `validate_completion` returned for the same `plan_run_id` + task; with no such row, rulings are ignored.
- One GitHub issue per finding: #105, #106, #107.

---

## File Structure

| File | Responsibility |
|------|----------------|
| `internal/secretgate/secretgate.go` (create) | `Mode`, `Gate`, `Refusal`; name rules and content rules. No I/O, no env. |
| `internal/secretgate/secretgate_test.go` (create) | Table tests, near-misses, the no-literal-secrets self-check |
| `internal/config/config.go` | `Config.SecretGate`, parsing the two env vars |
| `internal/config/config_test.go` | Parse and failure tests |
| `internal/mcpsrv/file_source.go` | `resolveFileInput` takes a `*secretgate.Gate` and calls both tiers |
| `internal/mcpsrv/{context_files,stale_comment_hint,worker_handlers,handlers}.go` | Pass `cfg.SecretGate` through |
| `internal/mcpsrv/secret_gate_test.go` (create) | Handler-level refusal tests, one per caller |
| `internal/planrun/planrun.go` | `UpsertResult`, `LiteState`, `IssuedIDs`, `AddIssuedIDs`, `resetCompletion` |
| `internal/planrun/planrun_test.go` | Store tests (two existing tests rewritten) |
| `internal/mcpsrv/plan_run_rows.go` | `liteRowState`, `sessionOwnedRowAdvisory`, `withIssuedIDs`, result switch |
| `internal/mcpsrv/finding_rulings.go` | `lightweightRulings` binding, two new advisories, `noSessionResponsesAdvisory` text |
| `internal/mcpsrv/handlers.go` | Lightweight branch wiring (around lines 2031–2050, 2190–2201) |
| `internal/mcpsrv/handlers_rulings_test.go`, `handlers_plan_run_rows_test.go` | Handler tests (two existing tests rewritten) |
| `README.md`, `docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md`, `docs/protocol/implementer.md` (+ bundle), `CHANGELOG.md`, plugin version files | Docs and release |

---

### Task 1: `internal/secretgate` package

**Goal:** A leaf package that refuses secret-looking paths (name tier) and bytes (content tier), with the rule sets of spec §3.2–§3.3.

**Files:**
- Create: `internal/secretgate/secretgate.go`
- Create: `internal/secretgate/secretgate_test.go`

**Acceptance Criteria:**
- [ ] `New(ModeOff, nil)` returns a nil `*Gate`; `CheckPath` / `CheckContent` on a nil `*Gate` return nil.
- [ ] Every name rule in spec §3.2 refuses its examples and passes its exceptions (`.env.example`, `.env.sample`, `.env.template`, `.env.dist`, `id_rsa.pub`, `.ssh/known_hosts`, `.ssh/authorized_keys`, `.ssh/config`).
- [ ] `CheckPath(requested, resolved)` refuses when either name matches, and the `Refusal.Path` is the matching one.
- [ ] Windows folding: with `goos == "windows"`, `C:\Repo\.ENV` is refused; on linux `.ENV` is not.
- [ ] Every content rule refuses a runtime-built sample and passes its near-miss; `ModeNames` skips the content tier.
- [ ] `Refusal.Error()` never contains the matched bytes.
- [ ] `New` returns an error for a malformed extra glob (`"[a-"`); an extra glob matches the basename or the full path.
- [ ] `TestSourceFilesPassTheContentTier`: every `*.go` file in the package passes `CheckContent` under `ModeOn`.

**Verify:** `go test -race ./internal/secretgate/...` → `ok`

**Steps:**

- [ ] **Step 1: Write the failing tests** in `internal/secretgate/secretgate_test.go`:

```go
package secretgate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gate(t *testing.T, mode Mode, extra ...string) *Gate {
	t.Helper()
	g, err := New(mode, extra)
	require.NoError(t, err)
	return g
}

func refusalOf(t *testing.T, err error) *Refusal {
	t.Helper()
	var r *Refusal
	require.True(t, errors.As(err, &r), "want a *Refusal, got %v", err)
	return r
}

func TestOffIsNil(t *testing.T) {
	g, err := New(ModeOff, []string{"*.txt"})
	require.NoError(t, err)
	assert.Nil(t, g)
	assert.NoError(t, g.CheckPath("/r/.env", "/r/.env"))
	assert.NoError(t, g.CheckContent("/r/x", []byte("AKIA"+strings.Repeat("Q", 16))))
}

func TestNameRules(t *testing.T) {
	g := gate(t, ModeOn)
	refused := map[string]string{
		"/r/.env":                           "dotenv",
		"/r/.env.production":                "dotenv",
		"/r/certs/server.pem":               "private-key-file",
		"/r/tls.key":                        "private-key-file",
		"/r/store.p12":                      "private-key-file",
		"/r/a.pfx":                          "private-key-file",
		"/r/a.jks":                          "private-key-file",
		"/r/a.keystore":                     "private-key-file",
		"/r/vault.kdbx":                     "private-key-file",
		"/home/u/id_ed25519":                "ssh-key",
		"/r/id_rsa":                         "ssh-key",
		"/home/u/.ssh/deploy":               "ssh-key",
		"/home/u/.netrc":                    "credential-file",
		"/home/u/_netrc":                    "credential-file",
		"/home/u/.git-credentials":          "credential-file",
		"/r/.npmrc":                         "credential-file",
		"/r/.pypirc":                        "credential-file",
		"/r/.htpasswd":                      "credential-file",
		"/home/u/.pgpass":                   "credential-file",
		"/home/u/.aws/credentials":          "cloud-credentials",
		"/home/u/.aws/config":               "cloud-credentials",
		"/home/u/.docker/config.json":       "cloud-credentials",
		"/home/u/.kube/config":              "cloud-credentials",
		"/home/u/.config/gcloud/credentials.db": "cloud-credentials",
		"/home/u/.config/gcloud/application_default_credentials.json": "cloud-credentials",
		"/home/u/.gnupg/pubring.kbx":        "gnupg",
		"/r/infra/terraform.tfstate":        "terraform-state",
		"/r/infra/terraform.tfstate.backup": "terraform-state",
		"/r/infra/prod.tfvars":              "terraform-state",
	}
	for p, rule := range refused {
		r := refusalOf(t, g.CheckPath(p, p))
		assert.Equal(t, "names", r.Tier, p)
		assert.Equal(t, rule, r.Rule, p)
		assert.Equal(t, p, r.Path, p)
	}
	allowed := []string{
		"/r/.env.example", "/r/.env.sample", "/r/.env.template", "/r/.env.dist",
		"/home/u/.ssh/id_rsa.pub", "/home/u/.ssh/known_hosts", "/home/u/.ssh/authorized_keys", "/home/u/.ssh/config",
		"/r/main.go", "/r/environment.go", "/r/docs/keys.md", "/r/config.json", "/r/.envrc.md",
	}
	for _, p := range allowed {
		assert.NoError(t, g.CheckPath(p, p), p)
	}
}

func TestCheckPathChecksBothNames(t *testing.T) {
	g := gate(t, ModeOn)
	r := refusalOf(t, g.CheckPath("/r/notes.txt", "/r/.env"))
	assert.Equal(t, "/r/.env", r.Path)
	r = refusalOf(t, g.CheckPath("/r/.env", "/r/notes.txt"))
	assert.Equal(t, "/r/.env", r.Path)
}

func TestWindowsFoldsCase(t *testing.T) {
	g := gate(t, ModeOn)
	assert.NoError(t, g.CheckPath("/r/.ENV", "/r/.ENV"), "case-sensitive off Windows")
	g.goos = "windows"
	r := refusalOf(t, g.CheckPath(`C:\Repo\.ENV`, `C:\Repo\.ENV`))
	assert.Equal(t, "dotenv", r.Rule)
}

func TestExtraGlobs(t *testing.T) {
	g := gate(t, ModeOn, "*.secret", "/r/private/*")
	assert.Equal(t, "extra", refusalOf(t, g.CheckPath("/r/a.secret", "/r/a.secret")).Rule)
	assert.Equal(t, "extra", refusalOf(t, g.CheckPath("/r/private/x.go", "/r/private/x.go")).Rule)
	assert.NoError(t, g.CheckPath("/r/public/x.go", "/r/public/x.go"))
	_, err := New(ModeOn, []string{"[a-"})
	assert.Error(t, err)
}

func pemSample() string {
	return "-----BEGIN " + "RSA PRIVATE" + " KEY-----\n" + strings.Repeat("MIIE", 16) + "\n-----END " + "RSA PRIVATE" + " KEY-----\n"
}

func TestContentRules(t *testing.T) {
	g := gate(t, ModeOn)
	refused := map[string]string{
		"pem-private-key":   pemSample(),
		"aws-access-key-id": "key = " + "AKIA" + strings.Repeat("Q", 16) + "\n",
		"github-token":      "token: " + "ghp" + "_" + strings.Repeat("a", 36),
		"slack-token":       "xox" + "b-" + strings.Repeat("1", 12),
		"anthropic-key":     "k=" + "sk-" + "ant-" + strings.Repeat("a", 24),
		"openai-key":        "k=" + "sk-" + "proj-" + strings.Repeat("a", 40),
		"google-api-key":    "k=" + "AI" + "za" + strings.Repeat("b", 35),
		"stripe-live-key":   "k=" + "sk" + "_live_" + strings.Repeat("c", 24),
	}
	for rule, sample := range refused {
		r := refusalOf(t, g.CheckContent("/r/f", []byte(sample)))
		assert.Equal(t, "content", r.Tier, rule)
		assert.Equal(t, rule, r.Rule, rule)
		assert.NotContains(t, r.Error(), sample, rule)
	}
	nearMisses := []string{
		"-----BEGIN " + "RSA PRIVATE" + " KEY----- appears in prose only",
		"AKIA" + strings.Repeat("Q", 15),
		"ghp" + "_" + strings.Repeat("a", 20),
		"sk-" + "short",
		"plain source code with no tokens",
	}
	for _, s := range nearMisses {
		assert.NoError(t, g.CheckContent("/r/f", []byte(s)), s)
	}
}

func TestNamesModeSkipsContent(t *testing.T) {
	g := gate(t, ModeNames)
	assert.NoError(t, g.CheckContent("/r/f", []byte(pemSample())))
	assert.Error(t, g.CheckPath("/r/.env", "/r/.env"))
}

func TestParseMode(t *testing.T) {
	for _, s := range []string{"on", "names", "off"} {
		m, err := ParseMode(s)
		require.NoError(t, err)
		assert.Equal(t, Mode(s), m)
	}
	_, err := ParseMode("yes")
	assert.Error(t, err)
}

// TestSourceFilesPassTheContentTier keeps this package's own sources free of
// key-shaped literals, so a diff of this repository passes its own gate.
func TestSourceFilesPassTheContentTier(t *testing.T) {
	g := gate(t, ModeOn)
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, f := range files {
		b, err := os.ReadFile(f)
		require.NoError(t, err)
		assert.NoError(t, g.CheckContent(f, b), f)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/secretgate/...`
Expected: FAIL, build error (`undefined: New`).

- [ ] **Step 3: Implement** `internal/secretgate/secretgate.go`:

```go
// Package secretgate decides whether a file the server is about to send to a
// model looks like a secret, by its name or by its bytes. It does no I/O and
// reads no environment; config builds the Gate and the file reader calls it.
//
// It stops accidents, not a determined caller: an agent that can read a file
// can paste its contents into a text field the gate never sees.
package secretgate

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Mode selects which tiers run.
type Mode string

const (
	ModeOn    Mode = "on"
	ModeNames Mode = "names"
	ModeOff   Mode = "off"
)

// ParseMode reads an ANTI_TANGENT_SECRET_GATE value.
func ParseMode(s string) (Mode, error) {
	switch m := Mode(strings.TrimSpace(s)); m {
	case ModeOn, ModeNames, ModeOff:
		return m, nil
	}
	return "", fmt.Errorf("%q is not one of on, names, off", s)
}

// Refusal is the error a refused file produces. It names the rule and never
// the matched bytes, so the error can reach a summary block or a log safely.
type Refusal struct {
	Path string
	Tier string // "names" or "content"
	Rule string
}

func (r *Refusal) Error() string {
	return fmt.Sprintf("%q refused by the secret gate (%s: %s); drop it, or set ANTI_TANGENT_SECRET_GATE to change the gate",
		r.Path, r.Tier, r.Rule)
}

// Gate holds the compiled rules. A nil *Gate checks nothing.
type Gate struct {
	content bool
	extra   []string
	goos    string
}

// New builds a Gate, or nil for ModeOff. extra are filepath.Match globs added
// to the name tier; a malformed one is an error.
func New(mode Mode, extra []string) (*Gate, error) {
	for _, g := range extra {
		if _, err := filepath.Match(g, ""); err != nil {
			return nil, fmt.Errorf("glob %q: %w", g, err)
		}
	}
	if mode == ModeOff {
		return nil, nil
	}
	return &Gate{content: mode == ModeOn, extra: extra, goos: runtime.GOOS}, nil
}

// CheckPath runs the name tier on both the path the caller sent and the path
// it resolved to, so a symlink cannot rename a secret in either direction.
func (g *Gate) CheckPath(requested, resolved string) error {
	if g == nil {
		return nil
	}
	for _, p := range []string{requested, resolved} {
		if rule := g.nameRule(p); rule != "" {
			return &Refusal{Path: p, Tier: "names", Rule: rule}
		}
	}
	return nil
}

// CheckContent runs the content tier on bytes already read and capped.
func (g *Gate) CheckContent(p string, b []byte) error {
	if g == nil || !g.content {
		return nil
	}
	for _, r := range contentRules {
		if r.re.Match(b) {
			return &Refusal{Path: p, Tier: "content", Rule: r.name}
		}
	}
	return nil
}

var (
	envTemplates   = map[string]bool{"example": true, "sample": true, "template": true, "dist": true}
	privateKeyExts = map[string]bool{".pem": true, ".key": true, ".p12": true, ".pfx": true, ".jks": true, ".keystore": true, ".kdbx": true}
	sshKeyNames    = map[string]bool{"id_rsa": true, "id_dsa": true, "id_ecdsa": true, "id_ed25519": true}
	sshPublic      = map[string]bool{"known_hosts": true, "authorized_keys": true, "config": true}
	credFiles      = map[string]bool{".netrc": true, "_netrc": true, ".git-credentials": true, ".npmrc": true, ".pypirc": true, ".htpasswd": true, ".pgpass": true}
	cloudSuffixes  = []string{"/.aws/credentials", "/.aws/config", "/.docker/config.json", "/.kube/config",
		"/gcloud/credentials.db", "/gcloud/application_default_credentials.json"}
	tfSuffixes = []string{".tfstate", ".tfstate.backup", ".tfvars"}
)

func (g *Gate) nameRule(p string) string {
	if g.goos == "windows" {
		p = strings.ToLower(strings.ReplaceAll(p, `\`, "/"))
	} else {
		p = filepath.ToSlash(p)
	}
	base := path.Base(p)
	switch {
	case base == ".env" || (strings.HasPrefix(base, ".env.") && !envTemplates[strings.TrimPrefix(base, ".env.")]):
		return "dotenv"
	case privateKeyExts[path.Ext(base)]:
		return "private-key-file"
	case sshKeyNames[base]:
		return "ssh-key"
	case strings.Contains(p, "/.ssh/") && !strings.HasSuffix(base, ".pub") && !sshPublic[base]:
		return "ssh-key"
	case credFiles[base]:
		return "credential-file"
	case hasAnySuffix(p, cloudSuffixes):
		return "cloud-credentials"
	case strings.Contains(p, "/.gnupg/"):
		return "gnupg"
	case hasAnySuffix(base, tfSuffixes):
		return "terraform-state"
	}
	for _, glob := range g.extra {
		if ok, _ := filepath.Match(glob, base); ok {
			return "extra"
		}
		if ok, _ := filepath.Match(glob, p); ok {
			return "extra"
		}
	}
	return ""
}

func hasAnySuffix(s string, suffixes []string) bool {
	for _, suf := range suffixes {
		if strings.HasSuffix(s, suf) {
			return true
		}
	}
	return false
}

type contentRule struct {
	name string
	re   *regexp.Regexp
}

// contentRules each anchor on a fixed vendor prefix, which keeps false
// positives rare. The PEM rule needs a base64 body after the armour line, so
// prose that only names the header passes. Order matters only for the rule
// name reported: anthropic-key is checked before the broader openai-key.
var contentRules = []contentRule{
	{"pem-private-key", regexp.MustCompile(`-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----\r?\n(?:[A-Za-z-]+:[^\n]*\r?\n)*(?:\r?\n)?[A-Za-z0-9+/=]{40,}`)},
	{"aws-access-key-id", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"github-token", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}|\bgithub_pat_[A-Za-z0-9_]{60,}`)},
	{"slack-token", regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}`)},
	{"anthropic-key", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}`)},
	{"openai-key", regexp.MustCompile(`\bsk-(?:proj-)?[A-Za-z0-9_-]{32,}`)},
	{"google-api-key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`)},
	{"stripe-live-key", regexp.MustCompile(`\b[rs]k_live_[0-9A-Za-z]{24,}`)},
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test -race ./internal/secretgate/...`
Expected: `ok`. If `TestSourceFilesPassTheContentTier` fails, a literal crept into a source file; split it with concatenation. Do not weaken the rule.

- [ ] **Step 5: Commit**

```bash
git add internal/secretgate
git commit -m "feat(secretgate): name and content tiers for files sent to a model"
```

```json:metadata
{"files": ["internal/secretgate/secretgate.go", "internal/secretgate/secretgate_test.go"], "verifyCommand": "go test -race ./internal/secretgate/...", "acceptanceCriteria": ["ModeOff yields a nil Gate that checks nothing", "every spec §3.2 name rule refuses its examples and passes its exceptions", "CheckPath refuses on either the requested or the resolved name", "windows folding refuses C:\\Repo\\.ENV, linux passes .ENV", "every content rule refuses a runtime-built sample and passes its near-miss; ModeNames skips content", "Refusal.Error never contains matched bytes", "New errors on a malformed extra glob; extra globs match basename or full path", "package sources pass the content tier"], "modelTier": "mechanical"}
```

---

### Task 2: Config: `ANTI_TANGENT_SECRET_GATE` and `ANTI_TANGENT_FILE_DENY_EXTRA`

**Goal:** `config.Load` builds `Config.SecretGate` from the two variables, defaulting to `on`, and fails on bad values.

**Files:**
- Modify: `internal/config/config.go` (struct field near `PlanRoots`, about line 55; parsing just before the `ANTI_TANGENT_PLAN_ROOTS` block, about line 407)
- Test: `internal/config/config_test.go`

**Acceptance Criteria:**
- [ ] Unset: `cfg.SecretGate` is non-nil and refuses `/r/.env` and a runtime-built PEM sample.
- [ ] `names`: refuses `/r/.env`, passes the PEM sample.
- [ ] `off`: `cfg.SecretGate == nil`.
- [ ] `ANTI_TANGENT_SECRET_GATE=yes` fails `Load` with an error naming the variable.
- [ ] `ANTI_TANGENT_FILE_DENY_EXTRA="*.secret<sep>/r/private/*"` (`<sep>` = `string(os.PathListSeparator)`) refuses both; a bad glob fails `Load` naming the variable.

**Verify:** `go test -race ./internal/config/...` → `ok`

**Steps:**

- [ ] **Step 1: Write the failing tests** (append to `internal/config/config_test.go`; match the file's existing `Load(func(k string) string {...})` helper style, and if the file has a map-backed helper, use it):

```go
func loadWith(t *testing.T, kv map[string]string) (Config, error) {
	t.Helper()
	base := map[string]string{"ANTHROPIC_API_KEY": "k"}
	for k, v := range kv {
		base[k] = v
	}
	return Load(func(k string) string { return base[k] })
}

func pemForTest() []byte {
	return []byte("-----BEGIN " + "EC PRIVATE" + " KEY-----\n" + strings.Repeat("MIIB", 16) + "\n")
}

func TestLoad_SecretGateDefaultsOn(t *testing.T) {
	cfg, err := loadWith(t, nil)
	require.NoError(t, err)
	require.NotNil(t, cfg.SecretGate)
	assert.Error(t, cfg.SecretGate.CheckPath("/r/.env", "/r/.env"))
	assert.Error(t, cfg.SecretGate.CheckContent("/r/x", pemForTest()))
}

func TestLoad_SecretGateNamesAndOff(t *testing.T) {
	cfg, err := loadWith(t, map[string]string{"ANTI_TANGENT_SECRET_GATE": "names"})
	require.NoError(t, err)
	assert.Error(t, cfg.SecretGate.CheckPath("/r/.env", "/r/.env"))
	assert.NoError(t, cfg.SecretGate.CheckContent("/r/x", pemForTest()))

	cfg, err = loadWith(t, map[string]string{"ANTI_TANGENT_SECRET_GATE": "off"})
	require.NoError(t, err)
	assert.Nil(t, cfg.SecretGate)
}

func TestLoad_SecretGateRejectsBadValues(t *testing.T) {
	_, err := loadWith(t, map[string]string{"ANTI_TANGENT_SECRET_GATE": "yes"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ANTI_TANGENT_SECRET_GATE")

	_, err = loadWith(t, map[string]string{"ANTI_TANGENT_FILE_DENY_EXTRA": "[a-"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ANTI_TANGENT_FILE_DENY_EXTRA")
}

func TestLoad_FileDenyExtra(t *testing.T) {
	sep := string(os.PathListSeparator)
	cfg, err := loadWith(t, map[string]string{"ANTI_TANGENT_FILE_DENY_EXTRA": "*.secret" + sep + " /r/private/* "})
	require.NoError(t, err)
	assert.Error(t, cfg.SecretGate.CheckPath("/r/a.secret", "/r/a.secret"))
	assert.Error(t, cfg.SecretGate.CheckPath("/r/private/x.go", "/r/private/x.go"))
	assert.NoError(t, cfg.SecretGate.CheckPath("/r/x.go", "/r/x.go"))
}
```

If `loadWith` or `pemForTest` collides with an existing helper name, rename the new one; do not change the existing one. Add the `os` / `strings` imports if they are missing.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/config/...`
Expected: FAIL, `cfg.SecretGate undefined`.

- [ ] **Step 3: Implement.** Add to the `Config` struct, right after `PlanRoots []string`:

```go
	// SecretGate refuses files that look like secrets, by name or by content,
	// on every path input before their bytes reach a model. Nil when
	// ANTI_TANGENT_SECRET_GATE=off.
	SecretGate *secretgate.Gate
```

Import `"github.com/patiently/anti-tangent-mcp/internal/secretgate"`. In `Load`, immediately before `if v := env("ANTI_TANGENT_PLAN_ROOTS"); v != "" {`:

```go
	gateMode := secretgate.ModeOn
	if v := env("ANTI_TANGENT_SECRET_GATE"); v != "" {
		m, err := secretgate.ParseMode(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_SECRET_GATE: %w", err)
		}
		gateMode = m
	}
	var denyExtra []string
	for _, g := range filepath.SplitList(env("ANTI_TANGENT_FILE_DENY_EXTRA")) {
		if g = strings.TrimSpace(g); g != "" {
			denyExtra = append(denyExtra, g)
		}
	}
	gate, err := secretgate.New(gateMode, denyExtra)
	if err != nil {
		return Config{}, fmt.Errorf("ANTI_TANGENT_FILE_DENY_EXTRA: %w", err)
	}
	cfg.SecretGate = gate
```

(If `err` is already declared in that scope, use `gate, gerr :=` instead.)

- [ ] **Step 4: Run to verify it passes**

Run: `go test -race ./internal/config/... && go build ./...`
Expected: `ok`, and the build succeeds.

- [ ] **Step 5: Commit**

```bash
git add internal/config
git commit -m "feat(config): ANTI_TANGENT_SECRET_GATE and ANTI_TANGENT_FILE_DENY_EXTRA"
```

```json:metadata
{"files": ["internal/config/config.go", "internal/config/config_test.go"], "verifyCommand": "go test -race ./internal/config/...", "acceptanceCriteria": ["unset: SecretGate non-nil, refuses /r/.env and a PEM sample", "names: refuses /r/.env, passes the PEM sample", "off: SecretGate nil", "bad ANTI_TANGENT_SECRET_GATE fails Load naming the var", "FILE_DENY_EXTRA globs refuse their matches; a bad glob fails Load naming the var"], "modelTier": "mechanical"}
```

---

### Task 3: Wire the gate into `resolveFileInput` and every path input

**Goal:** Every file the server reads goes through both tiers. A refusal is a transport error with no reviewer call, and the docs say so.

**Files:**
- Modify: `internal/mcpsrv/file_source.go:101` (`resolveFileInput`)
- Modify: every caller: `internal/mcpsrv/context_files.go:109`, `stale_comment_hint.go:160`, `worker_handlers.go:95` and `:217`, `handlers.go:1111` (`resolveTestEvidencePath`), `:1534`, `:1564`, `:2372`, and any test that calls `resolveFileInput` or `resolveTestEvidencePath` directly (the compiler lists them)
- Create: `internal/mcpsrv/secret_gate_test.go`
- Modify: tool schema strings in `internal/mcpsrv/handlers.go` for `context_paths` (all three tools), `final_files`, `final_diff_path`, `test_evidence_path`; and `internal/mcpsrv/tool_schema_contract_test.go` if it pins them
- Modify: `README.md` (env table near line 209; the `context_paths` bullets near line 350), `docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md` §3 (one paragraph)
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] `resolveFileInput(path string, roots []string, gate *secretgate.Gate, maxBytes int)` calls `gate.CheckPath(path, resolved)` after the roots check and before open, and `gate.CheckContent(resolved, b)` after the read; on refusal it returns the `*secretgate.Refusal` unwrapped and a zero `fileSource`.
- [ ] Every production caller passes `h.deps.Cfg.SecretGate` / `cfg.SecretGate`.
- [ ] `validate_task_spec` with `context_paths` naming a `.env` returns an error containing `secret gate` and `dotenv`, makes 0 reviewer calls, and creates no session.
- [ ] `validate_completion` returns an error with 0 reviewer calls for each of: `context_paths` with a `.env`; `final_files` path content carrying a runtime-built PEM; `final_diff_path` naming a file carrying a runtime-built AWS key id; `test_evidence_path` naming a `.env.local`.
- [ ] `validate_plan` with `plan_path` naming a gated file, and `bulk_read` with one gated path, each return an error.
- [ ] A symlink `notes.txt → .env` in `context_paths` is refused.
- [ ] With `cfg.SecretGate = nil`, the same `.env` call proceeds to the reviewer (one call).
- [ ] README documents both vars; schema text for the four inputs says a file the secret gate refuses fails the call; CHANGELOG has a `### Security` bullet for #105.

**Verify:** `go test -race ./internal/mcpsrv/... ./internal/secretgate/... ./internal/config/...` → `ok`

**Steps:**

- [ ] **Step 1: Write the failing handler tests** in `internal/mcpsrv/secret_gate_test.go`. Use the existing `newDeps`/`fakeReviewer` helpers; `fakeReviewer.Calls` counts reviewer calls.

```go
package mcpsrv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gateDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return d
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	return p
}

func pemBody() string {
	return "-----BEGIN " + "RSA PRIVATE" + " KEY-----\n" + strings.Repeat("MIIE", 16) + "\n"
}

func TestSecretGate_TaskSpecContextPathRefused(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("m")}
	h := &handlers{deps: newDeps(t, rv)}
	env := writeFile(t, gateDir(t), ".env", "A=1\n")
	_, out, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"}, ContextPaths: []string{env},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "secret gate")
	assert.Contains(t, err.Error(), "dotenv")
	assert.Empty(t, out.SessionID)
	assert.Equal(t, 0, rv.Calls)
}

func TestSecretGate_CompletionInputsRefused(t *testing.T) {
	dir := gateDir(t)
	cases := map[string]func(*ValidateCompletionArgs){
		"context_paths": func(a *ValidateCompletionArgs) { a.ContextPaths = []string{writeFile(t, dir, ".env", "A=1\n")} },
		"final_files": func(a *ValidateCompletionArgs) {
			a.FinalFiles = []CompletionFileArg{{Path: writeFile(t, dir, "k.txt", pemBody())}}
		},
		"final_diff_path": func(a *ValidateCompletionArgs) {
			a.FinalDiffPath = writeFile(t, dir, "d.diff", "+key = "+"AKIA"+strings.Repeat("Q", 16)+"\n")
		},
		"test_evidence_path": func(a *ValidateCompletionArgs) { a.TestEvidencePath = writeFile(t, dir, ".env.local", "ok\n") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			rv := &fakeReviewer{name: "anthropic", resp: passResp("m")}
			h := &handlers{deps: newDeps(t, rv)}
			args := completionCallArgs("")
			mutate(&args)
			_, _, err := h.ValidateCompletion(context.Background(), nil, args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "secret gate")
			assert.Equal(t, 0, rv.Calls)
		})
	}
}

func TestSecretGate_SymlinkToSecretRefused(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("m")}
	h := &handlers{deps: newDeps(t, rv)}
	dir := gateDir(t)
	target := writeFile(t, dir, ".env", "A=1\n")
	link := filepath.Join(dir, "notes.txt")
	require.NoError(t, os.Symlink(target, link))
	_, _, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"}, ContextPaths: []string{link},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dotenv")
}

func TestSecretGate_OffLetsTheFileThrough(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("m")}
	h := &handlers{deps: newDeps(t, rv)}
	h.deps.Cfg.SecretGate = nil
	env := writeFile(t, gateDir(t), ".env", "A=1\n")
	_, _, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"}, ContextPaths: []string{env},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, rv.Calls)
}
```

Add two more tests in the same file, following the existing patterns for those tools: `validate_plan` with `PlanPath` naming a gated file (copy the arg setup from a `plan_path` test in `handlers_plan_test.go`), and `bulk_read` with one gated path (copy the setup from `worker_handlers_test.go`). Each asserts `require.Error` and that the message contains `secret gate`. If `CompletionFileArg` needs `Content: nil` to make the server read the path, that is its zero value.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/mcpsrv/ -run TestSecretGate`
Expected: FAIL. The calls succeed, or the reviewer is called.

- [ ] **Step 3: Implement in `file_source.go`.** Change the signature and add both checks:

```go
func resolveFileInput(path string, roots []string, gate *secretgate.Gate, maxBytes int) (string, fileSource, error) {
	// ... unchanged through the withinRoots check ...
	if err := gate.CheckPath(path, resolved); err != nil {
		return "", fileSource{}, err
	}

	f, err := openNoFollow(resolved)
	// ... unchanged through the capped read and its error handling ...
	if err := gate.CheckContent(resolved, b); err != nil {
		return "", fileSource{}, err
	}

	sum := sha256.Sum256(b)
	// ... unchanged ...
}
```

Add the doc comment line to `resolveFileInput`: `// gate runs on both names before the open and on the bytes after the read; a refusal returns the *secretgate.Refusal itself.`

- [ ] **Step 4: Fix every caller.** Run `go build ./... 2>&1 | grep resolveFileInput` and add the gate argument at each site: `cfg.SecretGate` in `context_files.go` and `stale_comment_hint.go`, and `h.deps.Cfg.SecretGate` in `worker_handlers.go` and `handlers.go`. `resolveTestEvidencePath(path, roots, maxBytes)` becomes `resolveTestEvidencePath(path string, roots []string, gate *secretgate.Gate, maxBytes int)`, and its callers pass the gate too. The completion-input helper around `handlers.go:1520` that takes `roots` gets a `gate` parameter the same way. Fix test call sites with `nil` for the gate. Run `go vet ./...` until clean.

- [ ] **Step 5: Run to verify it passes**

Run: `go test -race ./internal/mcpsrv/...`
Expected: `ok`. If an existing test now fails because a fixture is key-shaped or named like a secret, set `deps.Cfg.SecretGate = nil` in that test only and say so in the task report. Never change a gate rule to make one pass.

- [ ] **Step 6: Docs and schema text.**
  - In the `jsonschema` strings for `context_paths` (three structs), `final_files`, `final_diff_path` and `test_evidence_path`, append: ` A file the secret gate refuses (ANTI_TANGENT_SECRET_GATE) fails the call.` Update `tool_schema_contract_test.go` wherever it pins the old strings.
  - README env block, after the `ANTI_TANGENT_PLAN_ROOTS=` line:

```text
ANTI_TANGENT_SECRET_GATE=on                   # on | names | off — refuse files that look like secrets on every path input (names: by file name only)
ANTI_TANGENT_FILE_DENY_EXTRA=                 # OS path-list separated filepath.Match globs added to the secret gate's name rules
```

  - README, after the `context_paths` bullet (line ~350), add a bullet: `- **Secret gate** (v0.29.0+): every file the server reads for a tool — context_paths, final_files, final_diff_path, test_evidence_path, plan_path, bulk_read and code_write reference paths — is refused before any bytes leave the host when its name looks like a secrets file (.env, *.pem, id_rsa, .netrc, .aws/credentials, …) or its contents carry a private key or a well-known token prefix. A refusal is a tool error naming the rule; the matched bytes are never echoed. ANTI_TANGENT_SECRET_GATE=names keeps only the name rules, off disables the gate. It stops accidental attachments, not a caller that pastes a secret into a text field.`
  - Design spec §3 (file inputs): one paragraph with the same content, pointing at `internal/secretgate`.
  - `CHANGELOG.md`: create `## [0.29.0] - 2026-10-11` above `## [0.28.0]` if absent, with a `### Security` subsection containing: `- A secret gate refuses any file the server is about to send to a model whose name looks like a secrets file or whose contents carry a private key or a well-known token, on every path input (context_paths, final_files, final_diff_path, test_evidence_path, plan_path, bulk_read, code_write's reference_path). A refusal is a tool error naming the rule, and no reviewer call is made. ANTI_TANGENT_SECRET_GATE (on, names, off; default on) and ANTI_TANGENT_FILE_DENY_EXTRA configure it. (#105)`

- [ ] **Step 7: Full check and commit**

Run: `go build ./... && go vet ./... && go test -race ./...`
Expected: all `ok`.

```bash
git add -A internal README.md docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md CHANGELOG.md
git commit -m "feat: secret gate on every file input before it reaches a model"
```

```json:metadata
{"files": ["internal/mcpsrv/file_source.go", "internal/mcpsrv/context_files.go", "internal/mcpsrv/stale_comment_hint.go", "internal/mcpsrv/worker_handlers.go", "internal/mcpsrv/handlers.go", "internal/mcpsrv/secret_gate_test.go", "internal/mcpsrv/tool_schema_contract_test.go", "README.md", "docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md", "CHANGELOG.md"], "verifyCommand": "go test -race ./...", "acceptanceCriteria": ["resolveFileInput runs CheckPath before open and CheckContent after read, returning the Refusal", "every production caller passes the configured gate", "validate_task_spec context_paths .env: error with 'secret gate' and 'dotenv', 0 reviewer calls, no session", "validate_completion context_paths/final_files/final_diff_path/test_evidence_path gated: error, 0 reviewer calls", "validate_plan plan_path and bulk_read gated: error", "symlink notes.txt -> .env refused", "nil gate lets the .env through to one reviewer call", "README, schema text and CHANGELOG Security bullet updated"], "modelTier": "standard"}
```

---

### Task 4: `planrun`: session rows refuse lite writes; `Attach` clears lite results; `IssuedIDs`

**Goal:** The store primitives for F8 and F3: `UpsertLite` reports and refuses a session-attached row, `Attach` resets a lite row's completion fields, `LiteState` reads a row without creating it, and `TaskRow.IssuedIDs` holds up to 200 IDs.

**Files:**
- Modify: `internal/planrun/planrun.go` (`TaskRow` ~line 39, `cloneRow` ~497, `Attach` ~600, `UpsertLite` ~648)
- Test: `internal/planrun/planrun_test.go` (rewrite `TestUpsertLite_UpdatesAnAttachedRowWithoutMarkingItLite` at line 130; extend `TestAttach_ClearsLiteOnARowALightweightCallCreated` at line 154)
- Modify: `internal/mcpsrv/plan_run_rows.go:286` (minimal compile fix only; Task 5 does the behaviour)

**Acceptance Criteria:**
- [ ] `UpsertLite` returns `(TaskRow, UpsertResult)`: `UpsertApplied` on a lite or new row; `UpsertUnknown` for an unknown run or an empty ref; `UpsertSessionOwned`, with `mutate` not called and the row unchanged, when the row has a `SessionID`.
- [ ] `Attach` on a row with `Lite == true` clears `PostVerdict`, `Severity`, `SubmissionOnly`, `Codescene`, `CompletedAt`, `Waived`, `Escalated`, `Categories`, `LinesAdded`, `LinesRemoved`, `OverBuildingRuled`, `RateDigest` and `IssuedIDs`, sets `CodesceneState = StateMissing`, and keeps `Calls`. A re-attach of a non-lite row keeps its fields.
- [ ] `LiteState(runID, ref)` returns `{Found:false}` for an unknown run or empty ref, `{Found:true}` with no row yet, and `SessionOwned` / a copy of `IssuedIDs` for an existing row, and never adds a row.
- [ ] `AddIssuedIDs` de-duplicates, skips `""`, and keeps the newest 200.
- [ ] `cloneRow` deep-copies `IssuedIDs`.

**Verify:** `go test -race ./internal/planrun/... && go build ./...` → `ok`

**Steps:**

- [ ] **Step 1: Write the failing tests.** Replace `TestUpsertLite_UpdatesAnAttachedRowWithoutMarkingItLite` with:

```go
func TestUpsertLite_LeavesASessionRowAlone(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.CreateWithTasks("pass", "rigorous", titledTasks("Task 1: Alpha"))
	attach := attacher(t, s, r.ID)
	attach("s1", "Alpha", "pass")
	_, ok := s.UpdateRow(r.ID, "s1", func(row *TaskRow) { row.PostVerdict = "warn" })
	require.True(t, ok)

	called := false
	row, res := s.UpsertLite(r.ID, TaskRef{Index: 1}, func(row *TaskRow) { called = true; row.PostVerdict = "pass" })
	assert.Equal(t, UpsertSessionOwned, res)
	assert.False(t, called)
	assert.Equal(t, "warn", row.PostVerdict)
	got, _ := s.Snapshot(r.ID)
	require.Len(t, got.Rows, 1)
	assert.Equal(t, "warn", got.Rows[0].PostVerdict)
	assert.False(t, got.Rows[0].Lite)
}

func TestUpsertLite_UnknownRunOrEmptyRef(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.CreateWithTasks("pass", "rigorous", titledTasks("Task 1: Alpha"))
	_, res := s.UpsertLite("pr_000000000000", TaskRef{Index: 1}, func(*TaskRow) {})
	assert.Equal(t, UpsertUnknown, res)
	_, res = s.UpsertLite(r.ID, TaskRef{}, func(*TaskRow) {})
	assert.Equal(t, UpsertUnknown, res)
}

func TestLiteState(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.CreateWithTasks("pass", "rigorous", titledTasks("Task 1: Alpha", "Task 2: Beta"))
	assert.Equal(t, LiteState{}, s.LiteState("pr_000000000000", TaskRef{Index: 1}))
	assert.Equal(t, LiteState{Found: true}, s.LiteState(r.ID, TaskRef{Index: 2}))
	got, _ := s.Snapshot(r.ID)
	assert.Empty(t, got.Rows, "LiteState adds no row")

	_, res := s.UpsertLite(r.ID, TaskRef{Index: 2}, func(row *TaskRow) { row.AddIssuedIDs([]string{"f_00000001"}) })
	require.Equal(t, UpsertApplied, res)
	st := s.LiteState(r.ID, TaskRef{Index: 2})
	assert.Equal(t, LiteState{Found: true, IssuedIDs: []string{"f_00000001"}}, st)
	st.IssuedIDs[0] = "mutated"
	assert.Equal(t, "f_00000001", s.LiteState(r.ID, TaskRef{Index: 2}).IssuedIDs[0], "a copy")

	attacher(t, s, r.ID)("s1", "Alpha", "pass")
	assert.True(t, s.LiteState(r.ID, TaskRef{Index: 1}).SessionOwned)
}

func TestAddIssuedIDs_DedupesAndCaps(t *testing.T) {
	var row TaskRow
	row.AddIssuedIDs([]string{"a", "", "a", "b"})
	assert.Equal(t, []string{"a", "b"}, row.IssuedIDs)
	ids := make([]string, 250)
	for i := range ids {
		ids[i] = fmt.Sprintf("f_%08x", i)
	}
	row.AddIssuedIDs(ids)
	require.Len(t, row.IssuedIDs, 200)
	assert.Equal(t, fmt.Sprintf("f_%08x", 249), row.IssuedIDs[199])
}
```

Extend `TestAttach_ClearsLiteOnARowALightweightCallCreated`. Make the lite mutate also set `row.Waived = 2`, `row.Severity = map[string]int{"major": 1}`, `row.RateDigest = &ratedigest.Digest{N: 5}`, `row.CodesceneState = StateRan`, `row.AddIssuedIDs([]string{"f_00000001"})` and `row.AppendCall(ToolCall{Tool: "validate_completion"})`. After the `Attach`, assert:

```go
	assert.Empty(t, row.PostVerdict)
	assert.Zero(t, row.Waived)
	assert.Nil(t, row.Severity)
	assert.Nil(t, row.RateDigest)
	assert.Nil(t, row.IssuedIDs)
	assert.Equal(t, StateMissing, row.CodesceneState)
	assert.Len(t, row.Calls, 1, "the call log is history and is kept")
```

Add `TestAttach_ReattachKeepsASessionRowsResults`: attach s1, `UpdateRow` sets `PostVerdict = "warn"`, attach s2 → `PostVerdict` is still `"warn"` and `Attempts == 2`.

Fix `TestUpsertLite_AddsALiteRowTitledFromThePlan` for the new return: `row, res := ...; require.Equal(t, UpsertApplied, res)`, and the same in any other `UpsertLite` caller in the package's tests.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/planrun/...`
Expected: FAIL, build errors (`UpsertSessionOwned`, `LiteState`, `AddIssuedIDs` undefined).

- [ ] **Step 3: Implement.** In `TaskRow`, after `RateDigest`:

```go
	// IssuedIDs is the finding IDs lightweight validate_completion calls on
	// this task returned, oldest first, capped at maxIssuedIDs. A lightweight
	// call's controller_rulings may name only these. In memory only.
	IssuedIDs []string `json:"-"`
```

Add near `maxCallLog`:

```go
// maxIssuedIDs bounds a row's IssuedIDs, so a task resubmitted without limit
// cannot grow its row without limit.
const maxIssuedIDs = 200
```

Add after `AppendCall`:

```go
// AddIssuedIDs records finding IDs a lightweight call returned, skipping
// empty and already-recorded ones and keeping the newest maxIssuedIDs.
func (row *TaskRow) AddIssuedIDs(ids []string) {
	seen := make(map[string]bool, len(row.IssuedIDs)+len(ids))
	for _, id := range row.IssuedIDs {
		seen[id] = true
	}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		row.IssuedIDs = append(row.IssuedIDs, id)
	}
	if over := len(row.IssuedIDs) - maxIssuedIDs; over > 0 {
		row.IssuedIDs = append([]string(nil), row.IssuedIDs[over:]...)
	}
}

// resetCompletion clears everything a validate_completion wrote to the row,
// keeping the call log. A session taking over a lightweight row starts from
// no result, so the row never shows a lightweight verdict unmarked.
func (row *TaskRow) resetCompletion() {
	row.PostVerdict = ""
	row.Severity = nil
	row.SubmissionOnly = false
	row.Codescene = nil
	row.CodesceneState = StateMissing
	row.CompletedAt = time.Time{}
	row.Waived = 0
	row.Escalated = false
	row.Categories = nil
	row.LinesAdded, row.LinesRemoved = 0, 0
	row.OverBuildingRuled = 0
	row.RateDigest = nil
	row.IssuedIDs = nil
}
```

In `cloneRow` add `row.IssuedIDs = append([]string(nil), row.IssuedIDs...)`. When the input is nil this yields nil, which keeps the `assert.Nil` above true.

In `Attach`, replace `row.Lite = false` with:

```go
	if row.Lite {
		row.resetCompletion()
		row.Lite = false
	}
```

Replace `UpsertLite` and add `LiteState`:

```go
// UpsertResult is what UpsertLite did.
type UpsertResult int

const (
	// UpsertApplied: mutate ran on the task's lite row, added if new.
	UpsertApplied UpsertResult = iota
	// UpsertUnknown: the run is unknown or expired, or ref names nothing.
	UpsertUnknown
	// UpsertSessionOwned: a validate_task_spec session owns the row; mutate
	// did not run.
	UpsertSessionOwned
)

// UpsertLite applies mutate to the row ref names, first adding a Lite row
// when the task has none: a lightweight validate_completion has no session to
// attach. A row a session owns is left untouched, so the report never shows a
// lightweight result as a session-backed one. mutate must not change Index.
func (s *Store) UpsertLite(runID string, ref TaskRef, mutate func(*TaskRow)) (TaskRow, UpsertResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[runID]
	if !ok || ref.empty() {
		return TaskRow{}, UpsertUnknown
	}
	pos, _ := r.rowFor(ref, true)
	if r.Rows[pos].SessionID != "" {
		return cloneRow(r.Rows[pos]), UpsertSessionOwned
	}
	mutate(&r.Rows[pos])
	r.LastAccessed = time.Now()
	return cloneRow(r.Rows[pos]), UpsertApplied
}

// LiteState is what a lightweight validate_completion needs to know about the
// row ref names before its review.
type LiteState struct {
	// Found: the run is live and ref names a task, whether or not it has a row.
	Found bool
	// SessionOwned: a validate_task_spec session is attached to the row.
	SessionOwned bool
	// IssuedIDs is a copy of the row's IssuedIDs.
	IssuedIDs []string
}

// LiteState reads the row ref names without adding one.
func (s *Store) LiteState(runID string, ref TaskRef) LiteState {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[runID]
	if !ok || ref.empty() {
		return LiteState{}
	}
	pos, _, _ := r.resolve(ref)
	if pos < 0 {
		return LiteState{Found: true}
	}
	row := r.Rows[pos]
	st := LiteState{Found: true, SessionOwned: row.SessionID != ""}
	if len(row.IssuedIDs) > 0 {
		st.IssuedIDs = append([]string(nil), row.IssuedIDs...)
	}
	return st
}
```

In `internal/mcpsrv/plan_run_rows.go:286`, keep behaviour identical for now:

```go
	if row, res := h.deps.PlanRuns.UpsertLite(args.PlanRunID, ref, update); res == planrun.UpsertApplied {
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test -race ./internal/planrun/... && go build ./... && go test -race ./internal/mcpsrv/...`
Expected: `ok`. An mcpsrv test that relied on a lite call rewriting a session row (the audit's sequence) now fails; Task 5 rewrites it, so note its name in the task report and leave it failing only if it is one of the tests Task 5 lists. Otherwise fix it here.

- [ ] **Step 5: Commit**

```bash
git add internal/planrun internal/mcpsrv/plan_run_rows.go
git commit -m "fix(planrun): lite writes skip session rows; attach clears lite results"
```

```json:metadata
{"files": ["internal/planrun/planrun.go", "internal/planrun/planrun_test.go", "internal/mcpsrv/plan_run_rows.go"], "verifyCommand": "go test -race ./internal/planrun/... && go build ./...", "acceptanceCriteria": ["UpsertLite returns Applied/Unknown/SessionOwned; SessionOwned does not call mutate", "Attach on a lite row clears every completion field, sets CodesceneState missing, keeps Calls; re-attach of a session row keeps fields", "LiteState reports Found/SessionOwned/copied IssuedIDs and never adds a row", "AddIssuedIDs dedupes, skips empty, keeps newest 200", "cloneRow deep-copies IssuedIDs"], "modelTier": "mechanical"}
```

---

### Task 5: Handler: lightweight calls on a session row get an advisory and change nothing (F8)

**Goal:** A lightweight `validate_completion` naming a task a session owns leaves the row unchanged, `rate_digest` included, and its envelope carries a minor `plan_run_id` advisory.

**Files:**
- Modify: `internal/mcpsrv/plan_run_rows.go` (`recordLightweightCompletionRow` ~line 279; add `liteRowState`, `sessionOwnedRowAdvisory`)
- Modify: `internal/mcpsrv/handlers.go` (lightweight branch ~2036–2051; advisory appends ~2190–2201)
- Test: `internal/mcpsrv/handlers_plan_run_rows_test.go`
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] Audit sequence: `specFor` task 1 → session completion with a `warn` reviewer response → `completeLite` with a `pass` response and a `rate_digest` on task 1 → the row is still `warn`, `Lite == false`, `RateDigest` unchanged; the lite envelope has exactly one finding with `criterion == "plan_run_id"` whose evidence contains `validate_task_spec session`; and `plan_run_report` renders the row as `warn`, not `pass`.
- [ ] Reverse sequence: `completeLite` `pass` on task 1 → `specFor` task 1 → the report row shows `open (pre: …)`, not `pass`.
- [ ] A lite call on a task with no session still records a `(lite)` row (existing tests stay green).
- [ ] The advisory never changes `env.Verdict`.
- [ ] CHANGELOG `### Security` bullet for #107.

**Verify:** `go test -race ./internal/mcpsrv/ -run 'PlanRun|Lite'` → `ok`

**Steps:**

- [ ] **Step 1: Write the failing tests** in `handlers_plan_run_rows_test.go`:

```go
func TestLiteCompletion_DoesNotRewriteASessionRow(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("m")}
	h := &handlers{deps: newDeps(t, rv)}
	run := titledRun(h, "Task 1: Alpha")
	pre := specFor(t, h, run.ID, planrun.TaskRef{Index: 1})
	rv.resp = reviewerFindingsResp(driftFinding)
	sessionEnv := completeSession(t, h, pre.SessionID)
	require.Equal(t, "warn", sessionEnv.Verdict)
	before := rowsOf(t, h, run.ID)[0]

	rv.resp = passResp("m")
	args := completionCallArgs("")
	args.PlanRunID, args.TaskIndex = run.ID, 1
	args.RateDigest = digestArg(t, `{"n":10,"before_k":5,"after_k":8,"kept":true}`)
	_, env, err := h.ValidateCompletion(context.Background(), nil, args)
	require.NoError(t, err)
	assert.Equal(t, "pass", env.Verdict, "the advisory never moves the verdict")
	adv := findingsWithCriterion(env.Findings, "plan_run_id")
	require.Len(t, adv, 1)
	assert.Contains(t, adv[0].Evidence, "validate_task_spec session")

	after := rowsOf(t, h, run.ID)[0]
	assert.Equal(t, "warn", after.PostVerdict)
	assert.False(t, after.Lite)
	assert.Equal(t, before.RateDigest, after.RateDigest)

	_, report, err := h.PlanRunReport(context.Background(), nil, PlanRunReportArgs{PlanRunID: run.ID})
	require.NoError(t, err)
	require.Len(t, report.Tasks, 1)
	assert.Equal(t, "warn", report.Tasks[0].PostVerdict)
}

func TestLiteThenSession_RowStartsOpen(t *testing.T) {
	h, run := alphaBetaRun(t)
	completeLite(t, h, run.ID, planrun.TaskRef{Index: 1})
	require.Equal(t, "pass", rowsOf(t, h, run.ID)[0].PostVerdict)
	specFor(t, h, run.ID, planrun.TaskRef{Index: 1})
	row := rowsOf(t, h, run.ID)[0]
	assert.Empty(t, row.PostVerdict)
	assert.False(t, row.Lite)
}
```

`digestArg` is the helper `rate_digest_test.go` uses. If the digest's own validation rejects this minimal literal (check `internal/ratedigest`), copy the full literal from `TestValidateCompletion_RateDigestRenderedAndStored`.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/mcpsrv/ -run 'DoesNotRewriteASessionRow|RowStartsOpen'`
Expected: FAIL on the missing advisory. The row assertions already pass after Task 4.

- [ ] **Step 3: Implement.** In `plan_run_rows.go`:

```go
// liteRowState reads, before the review, the plan-run row a lightweight
// validate_completion names. Zero when the call carries no plan_run_id.
func (h *handlers) liteRowState(args ValidateCompletionArgs) planrun.LiteState {
	if args.PlanRunID == "" {
		return planrun.LiteState{}
	}
	return h.deps.PlanRuns.LiteState(args.PlanRunID, planrun.TaskRef{Index: args.TaskIndex, Title: args.TaskTitle})
}

// sessionOwnedRowAdvisory reports a lightweight validate_completion naming a
// task a validate_task_spec session owns: its result is not recorded there.
func sessionOwnedRowAdvisory() verdict.Finding {
	return ignoredArgumentAdvisory("plan_run_id",
		"This task has a validate_task_spec session in the plan run, so a lightweight validate_completion is not recorded against it.",
		"Finish the task with validate_completion on its session_id.")
}
```

Replace the body of `recordLightweightCompletionRow` after `update := ...`:

```go
	row, res := h.deps.PlanRuns.UpsertLite(args.PlanRunID, ref, update)
	switch res {
	case planrun.UpsertApplied:
		h.appendPlanLedger(args.PlanRunID, row)
	case planrun.UpsertSessionOwned:
		slog.Warn("plan run lightweight update skipped; a session owns the task's row",
			"plan_run_id", args.PlanRunID, "task_index", row.Index)
	default:
		slog.Warn("plan run lightweight update skipped; run unknown or expired, or no task named",
			"plan_run_id", args.PlanRunID)
	}
```

In `handlers.go`, declare `var liteState planrun.LiteState` next to `var lightweightMalformedRulingIDs []string` (~2031). In the `if lightweight {` branch, before the rulings line (~2050), add `liteState = h.liteRowState(args)`. After the `lightweightPlanRunAdvisory` append (~2193), add:

```go
	if lightweight && liteState.SessionOwned {
		env.Findings = append(env.Findings, sessionOwnedRowAdvisory())
	}
```

CHANGELOG `### Security` bullet: `- A lightweight validate_completion no longer changes a plan_run_report row that a validate_task_spec session owns; it gets a minor plan_run_id advisory instead. A session that takes over a task a lightweight call recorded starts from no result, so a row never shows a lightweight verdict without its (lite) marker. (#107)`

- [ ] **Step 4: Run to verify it passes**

Run: `go test -race ./internal/mcpsrv/...`
Expected: `ok`. Any existing test that expected a lite call to update a session row encodes the defect: rewrite its assertions to the new behaviour, and list it in the task report.

- [ ] **Step 5: Commit**

```bash
git add internal/mcpsrv CHANGELOG.md
git commit -m "fix: a lightweight completion leaves a session's plan-run row alone"
```

```json:metadata
{"files": ["internal/mcpsrv/plan_run_rows.go", "internal/mcpsrv/handlers.go", "internal/mcpsrv/handlers_plan_run_rows_test.go", "CHANGELOG.md"], "verifyCommand": "go test -race ./internal/mcpsrv/...", "acceptanceCriteria": ["audit sequence: session warn row stays warn, Lite false, RateDigest unchanged; lite envelope has one plan_run_id advisory; report shows no pass", "reverse sequence: lite pass then specFor leaves the row with no PostVerdict", "lite call on a session-less task still records a (lite) row", "advisory never changes env.Verdict", "CHANGELOG Security bullet for #107"], "modelTier": "standard"}
```

---

### Task 6: Handler: lightweight rulings bound to issued IDs (F3)

**Goal:** A lightweight `validate_completion` honours a `controller_rulings` entry only when its base ID was returned by an earlier lightweight call on the same plan-run row. `same_as` resolves only through issued IDs, and every call records the IDs it returned.

**Files:**
- Modify: `internal/mcpsrv/finding_rulings.go` (`lightweightRulings` ~606–619; `noSessionResponsesAdvisory` ~507–516; add two advisories)
- Modify: `internal/mcpsrv/handlers.go` (lightweight branch ~2050; advisory appends ~2196)
- Modify: `internal/mcpsrv/plan_run_rows.go` (`withIssuedIDs`; use it in `recordLightweightCompletionRow`)
- Modify: `internal/mcpsrv/handlers.go` schema string for `ValidateCompletionArgs.ControllerRulings` (~line 1239), and `tool_schema_contract_test.go` if it pins it
- Test: `internal/mcpsrv/handlers_rulings_test.go` (rewrite `TestValidateCompletion_WithoutASessionIgnoresAnswersButAppliesRulings` ~473 and `TestValidateCompletion_LightweightAppliesAControllerRuling` ~492; adjust the assertion message in `TestValidateCompletion_LightweightResponsesPointAtRulings` ~520)
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] No `plan_run_id`: a ruling is ignored, `env.ControllerRulings` is empty, the finding stands, the summary has no `ruling:` line, and there is one `controller_rulings` advisory whose evidence contains `names no such task`.
- [ ] With a run: call 1 returns `f_X`; call 2 with a ruling on `f_X` waives it (`WaivedFindings[0].ID == f_X`, the prompt carries `## Controller rulings (authoritative)`).
- [ ] Pre-emptive: on a fresh row, a ruling on `verdict.Fingerprint("scope_drift", "", "AC 1")` is ignored, the finding stands, and the advisory evidence lists that ID and contains `not returned by an earlier validate_completion`.
- [ ] `same_as`: after call 1 issues `f_X` and call 2 rules on it, a reviewer finding on a different criterion with `same_as: f_X` is waived. On a fresh row, a ruled-but-unissued ID named in `same_as` waives nothing.
- [ ] On a session-owned row, rulings are ignored and only the `plan_run_id` advisory appears (no second `controller_rulings` advisory).
- [ ] After each lightweight call on a row, `LiteState(...).IssuedIDs` contains every ID in `envelopeIDs(env)`.
- [ ] `noSessionResponsesAdvisory().Suggestion` no longer says rulings apply without a session, and still mentions `controller_rulings`.
- [ ] CHANGELOG `### Security` bullet for #106.

**Verify:** `go test -race ./internal/mcpsrv/ -run 'Ruling|Lightweight'` → `ok`

**Steps:**

- [ ] **Step 1: Write the failing tests** in `handlers_rulings_test.go`. Replace `TestValidateCompletion_WithoutASessionIgnoresAnswersButAppliesRulings` with:

```go
// TestValidateCompletion_WithoutAPlanRunRowIgnoresAnswersAndRulings: with no
// session and no plan-run row, nothing issued an id a ruling could name.
func TestValidateCompletion_WithoutAPlanRunRowIgnoresAnswersAndRulings(t *testing.T) {
	h, rv := newRulingsHandlers(t)
	args := completionCallArgs("")
	args.FindingResponses = []FindingResponseArg{{FindingID: "f_0123abcd", Response: "a"}}
	args.ControllerRulings = []ControllerRulingArg{{FindingID: "f_0123abcd", Ruling: "r"}}
	env := completeWith(t, h, rv, args, passResp("claude-opus-4-7"))

	require.Len(t, findingsWithCriterion(env.Findings, "session_id"), 1)
	adv := findingsWithCriterion(env.Findings, "controller_rulings")
	require.Len(t, adv, 1)
	assert.Contains(t, adv[0].Evidence, "names no such task")
	assert.Empty(t, env.ControllerRulings)
	assert.NotContains(t, env.SummaryBlock, "ruling:")
}
```

Replace `TestValidateCompletion_LightweightAppliesAControllerRuling` with these, and add the rest:

```go
func liteArgs(runID string) ValidateCompletionArgs {
	a := completionCallArgs("")
	a.PlanRunID, a.TaskIndex = runID, 1
	return a
}

func TestValidateCompletion_LightweightRulingOnAnIssuedIDApplies(t *testing.T) {
	h, rv := newRulingsHandlers(t)
	run := titledRun(h, "Task 1: Alpha")
	first := completeWith(t, h, rv, liteArgs(run.ID), reviewerFindingsResp(driftFinding))
	require.Len(t, first.Findings, 1)
	id := first.Findings[0].ID

	ruled := liteArgs(run.ID)
	ruled.ControllerRulings = []ControllerRulingArg{{FindingID: id, Ruling: "Task 7 owns the dispatcher wiring"}}
	env := completeWith(t, h, rv, ruled, reviewerFindingsResp(driftFinding))

	require.Len(t, env.WaivedFindings, 1)
	assert.Equal(t, id, env.WaivedFindings[0].ID)
	assert.Empty(t, findingsWithCriterion(env.Findings, "AC 1"))
	assert.Contains(t, rv.LastRequest.User, "## Controller rulings (authoritative)")
	assert.Contains(t, env.SummaryBlock, "ruling:")
}

func TestValidateCompletion_LightweightPreemptiveRulingIsIgnored(t *testing.T) {
	h, rv := newRulingsHandlers(t)
	run := titledRun(h, "Task 1: Alpha")
	guessed := verdict.Fingerprint("scope_drift", "", "AC 1")
	args := liteArgs(run.ID)
	args.ControllerRulings = []ControllerRulingArg{{FindingID: guessed, Ruling: "self-issued"}}
	env := completeWith(t, h, rv, args, reviewerFindingsResp(driftFinding))

	assert.Empty(t, env.WaivedFindings)
	require.Len(t, findingsWithCriterion(env.Findings, "AC 1"), 1, "the finding stands")
	adv := findingsWithCriterion(env.Findings, "controller_rulings")
	require.Len(t, adv, 1)
	assert.Contains(t, adv[0].Evidence, "not returned by an earlier validate_completion")
	assert.Contains(t, adv[0].Evidence, guessed)
}

func TestValidateCompletion_LightweightSameAsReachesOnlyThroughIssuedIDs(t *testing.T) {
	h, rv := newRulingsHandlers(t)
	run := titledRun(h, "Task 1: Alpha")
	id := completeWith(t, h, rv, liteArgs(run.ID), reviewerFindingsResp(driftFinding)).Findings[0].ID

	ruled := liteArgs(run.ID)
	ruled.ControllerRulings = []ControllerRulingArg{{FindingID: id, Ruling: "settled"}}
	other := findingObj("major", "quality", "AC 2", "same thing again", id)
	env := completeWith(t, h, rv, ruled, reviewerFindingsResp(other))
	require.Len(t, env.WaivedFindings, 1, "same_as through an issued, ruled id waives")

	fresh := titledRun(h, "Task 1: Beta")
	guessed := verdict.Fingerprint("scope_drift", "", "AC 1")
	args := liteArgs(fresh.ID)
	args.ControllerRulings = []ControllerRulingArg{{FindingID: guessed, Ruling: "self-issued"}}
	env = completeWith(t, h, rv, args, reviewerFindingsResp(findingObj("major", "quality", "AC 2", "x", guessed)))
	assert.Empty(t, env.WaivedFindings, "same_as through an unissued id waives nothing")
}

func TestValidateCompletion_LightweightRulingOnASessionRowIsIgnored(t *testing.T) {
	h, rv := newRulingsHandlers(t)
	run := titledRun(h, "Task 1: Alpha")
	specFor(t, h, run.ID, planrun.TaskRef{Index: 1})
	args := liteArgs(run.ID)
	args.ControllerRulings = []ControllerRulingArg{{FindingID: "f_0123abcd", Ruling: "r"}}
	env := completeWith(t, h, rv, args, reviewerFindingsResp(driftFinding))

	assert.Empty(t, env.WaivedFindings)
	assert.Len(t, findingsWithCriterion(env.Findings, "plan_run_id"), 1)
	assert.Empty(t, findingsWithCriterion(env.Findings, "controller_rulings"))
}

func TestValidateCompletion_LightweightRecordsIssuedIDs(t *testing.T) {
	h, rv := newRulingsHandlers(t)
	run := titledRun(h, "Task 1: Alpha")
	env := completeWith(t, h, rv, liteArgs(run.ID), reviewerFindingsResp(driftFinding))
	st := h.deps.PlanRuns.LiteState(run.ID, planrun.TaskRef{Index: 1})
	for _, id := range envelopeIDs(env) {
		assert.Contains(t, st.IssuedIDs, id)
	}
}
```

`verdict.Fingerprint` takes a `verdict.Category`; if `"scope_drift"` does not convert implicitly, use the package's constant (for example `verdict.CategoryScopeDrift`). `findingObj(severity, category, criterion, evidence, sameAs)` is the existing helper at the top of this file. Add the `internal/planrun` and `internal/verdict` imports if the file lacks them. In `TestValidateCompletion_LightweightResponsesPointAtRulings`, change only the failure message `"rulings work without a session; only responses are ignored"` to `"only responses are reported under session_id"`.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/mcpsrv/ -run 'Ruling|Lightweight'`
Expected: FAIL. The no-run and pre-emptive tests waive or apply what they should ignore.

- [ ] **Step 3: Implement in `finding_rulings.go`.** Replace `lightweightRulings`:

```go
// lightweightRulings builds a lightweight validate_completion review's ruling
// state. A lightweight task has no session, so its plan-run row stands in for
// one: a ruling applies only when its base ID is among issued, the IDs earlier
// lightweight calls on that row returned. shown is the issued set, so a
// reviewer's same_as reaches a finding only through an ID the server showed.
// unissued lists the well-formed ruling IDs that were dropped, sorted.
func lightweightRulings(rulingArgs []ControllerRulingArg, issued []string) (rulings map[string]session.Ruling, shown map[string]bool, malformed, unissued []string) {
	all, malformed := planRulings(rulingArgs)
	shown = make(map[string]bool, len(issued))
	issuedBase := make(map[string]bool, len(issued))
	for _, id := range issued {
		shown[id] = true
		issuedBase[verdict.BaseID(id)] = true
	}
	rulings = make(map[string]session.Ruling, len(all))
	for base, r := range all {
		if issuedBase[base] {
			rulings[base] = r
			continue
		}
		unissued = appendUnique(unissued, truncate(r.ID, maxEchoedIDRunes))
	}
	sort.Strings(unissued)
	return rulings, shown, malformed, unissued
}

// unboundLiteRulingsAdvisory reports controller_rulings on a lightweight call
// that names no plan-run task: no earlier call issued an ID they could name.
func unboundLiteRulingsAdvisory() verdict.Finding {
	return ignoredArgumentAdvisory("controller_rulings",
		"controller_rulings on a lightweight validate_completion apply only to finding ids an earlier lightweight validate_completion returned for the same plan_run_id and task; this call names no such task, so they were ignored.",
		"Resubmit with plan_run_id and task_index (or task_title) naming the task whose earlier validate_completion returned the ids.")
}

// unissuedLiteRulingsAdvisory reports lightweight ruling IDs no earlier call
// on the row returned. The IDs passed ValidDisplayID, so they are safe to echo.
func unissuedLiteRulingsAdvisory(ids []string) verdict.Finding {
	return ignoredArgumentAdvisory("controller_rulings",
		"These controller_rulings ids were not returned by an earlier validate_completion on this task, so they were ignored: "+strings.Join(ids, ", ")+".",
		"Rule only on ids a validate_completion response for this task showed.")
}
```

Change `noSessionResponsesAdvisory`. In the doc comment, replace the last sentence with `A controller ruling settles a finding here when the task is in a plan run.` Replace the suggestion string with:

```go
		"Fix the finding, or, on a task in a plan run, have the controller rule on it and resubmit with plan_run_id, task_index and controller_rulings.")
```

- [ ] **Step 4: Wire it in `handlers.go`.** Declare `var lightweightUnissuedRulingIDs []string` beside `lightweightMalformedRulingIDs`. Replace the lightweight ruling line and its comment (~2047–2050):

```go
		// A lightweight task has no session to remember the IDs it was shown,
		// so its plan-run row does: rulings may name only those.
		var issued []string
		if liteState.Found && !liteState.SessionOwned {
			issued = liteState.IssuedIDs
		}
		review.rulings, review.shown, lightweightMalformedRulingIDs, lightweightUnissuedRulingIDs = lightweightRulings(rulingArgs, issued)
```

(`liteState = h.liteRowState(args)` from Task 5 must come before this.) After the `malformedPlanRulingsAdvisory` append (~2198):

```go
	if len(lightweightUnissuedRulingIDs) > 0 {
		switch {
		case liteState.SessionOwned:
			// sessionOwnedRowAdvisory already says where the task's rulings belong.
		case !liteState.Found:
			env.Findings = append(env.Findings, unboundLiteRulingsAdvisory())
		default:
			env.Findings = append(env.Findings, unissuedLiteRulingsAdvisory(lightweightUnissuedRulingIDs))
		}
	}
```

In `plan_run_rows.go`, add:

```go
// withIssuedIDs wraps a lightweight row update so it also records the finding
// IDs the call returned, which the task's later controller_rulings may name.
func withIssuedIDs(update func(*planrun.TaskRow), ids []string) func(*planrun.TaskRow) {
	return func(row *planrun.TaskRow) {
		update(row)
		row.AddIssuedIDs(ids)
	}
}
```

and in `recordLightweightCompletionRow`, wrap the update: `update := withIssuedIDs(withRateDigest(countOverBuildingRuled(completionRowUpdate(env, args.Codescene, args.FinalDiff), overBuildingRuled), rd), envelopeIDs(env))`.

Schema string for `ValidateCompletionArgs.ControllerRulings`: append ` On a lightweight call (no session_id) a ruling applies only with plan_run_id and task_index or task_title, and only to an id an earlier validate_completion on that task returned.` Update `tool_schema_contract_test.go` if it pins the string.

CHANGELOG `### Security` bullet: `- A lightweight validate_completion applies a controller_rulings entry only when the call names its plan-run task (plan_run_id with task_index or task_title) and the ruled id was returned by an earlier validate_completion on that task; otherwise the ruling is ignored with a minor advisory. A reviewer's same_as reaches a finding only through such an id. Before, any well-formed id was honoured, including one guessed before the finding was ever shown. (#106)`

- [ ] **Step 5: Run to verify it passes**

Run: `go build ./... && go vet ./... && go test -race ./...`
Expected: all `ok`. Also run `go test ./internal/mcpsrv/... -run Replay` to confirm the replay dry run is unaffected.

- [ ] **Step 6: Commit**

```bash
git add internal/mcpsrv CHANGELOG.md
git commit -m "fix: lightweight rulings apply only to finding ids issued on the task's row"
```

```json:metadata
{"files": ["internal/mcpsrv/finding_rulings.go", "internal/mcpsrv/handlers.go", "internal/mcpsrv/plan_run_rows.go", "internal/mcpsrv/handlers_rulings_test.go", "internal/mcpsrv/tool_schema_contract_test.go", "CHANGELOG.md"], "verifyCommand": "go test -race ./...", "acceptanceCriteria": ["no plan_run_id: ruling ignored, no ruling: line, one controller_rulings advisory 'names no such task'", "with a run: a ruling on an id call 1 returned waives it on call 2", "pre-emptive ruling on a guessed fingerprint is ignored with the unissued advisory listing it", "same_as waives only through an issued ruled id", "session-owned row: rulings ignored, only the plan_run_id advisory", "IssuedIDs contains every envelopeIDs id after each lite call", "noSessionResponsesAdvisory suggestion no longer says rulings apply without a session", "CHANGELOG Security bullet for #106"], "modelTier": "standard"}
```

---

### Task 7: Protocol text, plugin bundle and release notes

**Goal:** The implementer protocol states the lightweight ruling rule within the byte budget, the plugin bundle matches, and the 0.29.0 changelog is complete.

**Files:**
- Modify: `docs/protocol/implementer.md` (Lightweight protocol mode, ~line 155)
- Modify: `plugin/anti-tangent-protocol/protocol/*.md` (resync)
- Modify: `plugin/anti-tangent-protocol/.claude-plugin/plugin.json`, `.claude-plugin/marketplace.json` (`0.4.0` → `0.4.1`)
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] `implementer.md` says a lightweight call's `controller_rulings` need `plan_run_id` + `task_index` and cover only IDs an earlier call on that task returned.
- [ ] `wc -c docs/protocol/implementer.md` < 16000.
- [ ] `diff -r docs/protocol plugin/anti-tangent-protocol/protocol` prints nothing.
- [ ] Both JSON files carry `"version": "0.4.1"` for the protocol plugin.
- [ ] CHANGELOG 0.29.0 has a `### Changed` bullet for `anti-tangent-protocol` 0.4.1 and for the `noSessionResponsesAdvisory` wording; no other section is missing an issue reference.

**Verify:** `wc -c docs/protocol/implementer.md && diff -r docs/protocol plugin/anti-tangent-protocol/protocol && go test -race ./...` → size < 16000, no diff output, `ok`

**Steps:**

- [ ] **Step 1: Edit `implementer.md`.** Replace this sentence in the Lightweight protocol mode section:

`Controllers may dispatch a **lightweight clause**: skip `validate_task_spec`, skip `check_progress`, keep `validate_completion` as the sanity gate (its handler accepts an empty `session_id` when any of `final_files` / `final_diff` / `test_evidence` is non-empty).`

with:

`Controllers may dispatch a **lightweight clause**: skip `validate_task_spec` and `check_progress`; keep `validate_completion` (empty `session_id`, with evidence). Its `controller_rulings` need `plan_run_id` + `task_index`, and cover only ids an earlier call on that task returned.`

- [ ] **Step 2: Check the budget**

Run: `wc -c docs/protocol/implementer.md`
Expected: < 16000. If it is over, shorten wording elsewhere in the same section (for example, the sentence beginning "Use the full protocol for …") until it fits. Keep every rule; change only wording. Do not renumber sections.

- [ ] **Step 3: Resync the bundle**

```bash
rm -f plugin/anti-tangent-protocol/protocol/*.md
cp docs/protocol/*.md plugin/anti-tangent-protocol/protocol/
diff -r docs/protocol plugin/anti-tangent-protocol/protocol
```

Expected: no output.

- [ ] **Step 4: Bump the plugin version** to `0.4.1` in `plugin/anti-tangent-protocol/.claude-plugin/plugin.json` and in the protocol plugin's entry in `.claude-plugin/marketplace.json`. Check other entries in marketplace.json and leave them unchanged.

- [ ] **Step 5: CHANGELOG `### Changed`** under `## [0.29.0]`:

```markdown
### Changed

- `anti-tangent-protocol` 0.4.1: `implementer.md` says a lightweight call's `controller_rulings` need `plan_run_id` and `task_index`, and cover only ids an earlier call on that task returned.
- The advisory for `finding_responses` sent without a session no longer says a ruling applies without one; it points at ruling on a task in a plan run.
```

- [ ] **Step 6: Full check and commit**

Run: `go build ./... && go vet ./... && go test -race ./...`
Expected: all `ok`.

```bash
git add docs/protocol plugin/anti-tangent-protocol .claude-plugin/marketplace.json CHANGELOG.md
git commit -m "docs(protocol): lightweight rulings need a plan-run task; 0.29.0 notes"
```

```json:metadata
{"files": ["docs/protocol/implementer.md", "plugin/anti-tangent-protocol/protocol/implementer.md", "plugin/anti-tangent-protocol/.claude-plugin/plugin.json", ".claude-plugin/marketplace.json", "CHANGELOG.md"], "verifyCommand": "wc -c docs/protocol/implementer.md && diff -r docs/protocol plugin/anti-tangent-protocol/protocol && go test -race ./...", "acceptanceCriteria": ["implementer.md states the lightweight ruling binding rule", "implementer.md under 16000 bytes", "plugin protocol bundle identical to docs/protocol", "protocol plugin version 0.4.1 in plugin.json and marketplace.json", "CHANGELOG 0.29.0 Changed bullets for the protocol and advisory wording"], "modelTier": "mechanical"}
```

---

## After the last task

Final whole-branch review (session model), then `superpowers-extended-cc:finishing-a-development-branch`. The PR to `main` is titled with `[minor]`, its body says "Closes #105, closes #106, closes #107", and the merge commit carries `[minor]`. The plan is not run through anti-tangent (Global Constraints), so there is no `plan_run_id` and no `record_review_outcome` call for this run; say so in the PR body.
