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
