package secretgate

import (
	"encoding/base64"
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
		"/r/.env":                               "dotenv",
		"/r/.env.production":                    "dotenv",
		"/r/.envrc":                             "dotenv",
		"/r/deploy/production.env":              "dotenv",
		"/r/certs/server.pem":                   "private-key-file",
		"/r/tls.key":                            "private-key-file",
		"/r/store.p12":                          "private-key-file",
		"/r/a.pfx":                              "private-key-file",
		"/r/a.jks":                              "private-key-file",
		"/r/a.keystore":                         "private-key-file",
		"/r/vault.kdbx":                         "private-key-file",
		"/home/u/id_ed25519":                    "ssh-key",
		"/r/id_rsa":                             "ssh-key",
		"/home/u/.ssh/deploy":                   "ssh-key",
		"/home/u/.netrc":                        "credential-file",
		"/home/u/_netrc":                        "credential-file",
		"/home/u/.git-credentials":              "credential-file",
		"/r/.npmrc":                             "credential-file",
		"/r/.pypirc":                            "credential-file",
		"/r/.htpasswd":                          "credential-file",
		"/home/u/.pgpass":                       "credential-file",
		"/home/u/.aws/credentials":              "cloud-credentials",
		"/home/u/.aws/config":                   "cloud-credentials",
		"/home/u/.docker/config.json":           "cloud-credentials",
		"/home/u/.kube/config":                  "cloud-credentials",
		"/home/u/.config/gcloud/credentials.db": "cloud-credentials",
		"/home/u/.config/gcloud/application_default_credentials.json": "cloud-credentials",
		"/home/u/.gnupg/pubring.kbx":                                  "gnupg",
		"/r/infra/terraform.tfstate":                                  "terraform-state",
		"/r/infra/terraform.tfstate.backup":                           "terraform-state",
		"/r/infra/prod.tfvars":                                        "terraform-state",
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
		"/r/main.go", "/r/environment.go", "/r/docs/keys.md", "/r/config.json", "/r/.envrc.md", "/r/foo.env.example", "/r/foo.env.sample",
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
	g, err := newForOS(ModeOn, nil, "linux")
	require.NoError(t, err)
	assert.NoError(t, g.CheckPath("/r/.ENV", "/r/.ENV"), "case-sensitive off Windows and macOS")
	g.goos = "windows"
	r := refusalOf(t, g.CheckPath(`C:\Repo\.ENV`, `C:\Repo\.ENV`))
	assert.Equal(t, "dotenv", r.Rule)
}

func TestDarwinFoldsCase(t *testing.T) {
	g, err := newForOS(ModeOn, nil, "darwin")
	require.NoError(t, err)
	assert.Equal(t, "dotenv", refusalOf(t, g.CheckPath("/r/.ENV", "/r/.ENV")).Rule)
	assert.NoError(t, g.CheckPath("/r/main.go", "/r/main.go"))
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
	for _, s := range []string{"OFF", " Names ", "On"} {
		m, err := ParseMode(s)
		require.NoError(t, err)
		assert.Equal(t, Mode(strings.ToLower(strings.TrimSpace(s))), m)
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

func TestExtraGlobsOnWindows(t *testing.T) {
	g, err := newForOS(ModeOn, []string{`C:\Repo\Private\*`, `*.SECRET`}, "windows")
	require.NoError(t, err)
	assert.Equal(t, "extra", refusalOf(t, g.CheckPath(`c:\repo\private\x.go`, `c:\repo\private\x.go`)).Rule)
	assert.Equal(t, "extra", refusalOf(t, g.CheckPath(`C:\Repo\Private\X.go`, `C:\Repo\Private\X.go`)).Rule)
	assert.Equal(t, "extra", refusalOf(t, g.CheckPath(`D:\a.Secret`, `D:\a.Secret`)).Rule)
	assert.NoError(t, g.CheckPath(`C:\Repo\Public\x.go`, `C:\Repo\Public\x.go`))
}

func pemParts() (begin, body, end string) {
	return "-----BEGIN " + "PRIVATE" + " KEY-----", strings.Repeat("MIIE", 16), "-----END " + "PRIVATE" + " KEY-----"
}

func TestPEMInOtherCarriers(t *testing.T) {
	g := gate(t, ModeOn)
	begin, body, end := pemParts()
	samples := map[string]string{
		"json-escaped":  `{"private_key": "` + begin + `\n` + body + `\n` + end + `\n"}`,
		"json-header":   `"k": "` + begin + `\nProc-Type: 4,ENCRYPTED\n\n` + body + `"`,
		"added":         "+" + begin + "\n+" + body + "\n+" + end + "\n",
		"removed":       "-" + begin + "\n-" + body + "\n-" + end + "\n",
		"context":       " " + begin + "\n " + body + "\n " + end + "\n",
		"removed-hdr":   "-" + begin + "\n-Proc-Type: 4,ENCRYPTED\n-\n-" + body + "\n",
		"crlf":          begin + "\r\n" + body + "\r\n",
		"yaml-2sp":      "key: |\n  " + begin + "\n  " + body + "\n  " + end + "\n",
		"yaml-tab":      "key: |\n\t" + begin + "\n\t" + body + "\n",
		"diff-yaml":     "+  key: |\n+    " + begin + "\n+    " + body + "\n",
		"json-crlf":     `{"k": "` + begin + `\r\n` + body + `\r\n` + end + `\r\n"}`,
		"json-crlf-hdr": `"k": "` + begin + `\r\nProc-Type: 4,ENCRYPTED\r\n\r\n` + body + `"`,
	}
	for name, sample := range samples {
		r := refusalOf(t, g.CheckContent("/r/f", []byte(sample)))
		assert.Equal(t, "pem-private-key", r.Rule, name)
	}
	prose := []string{
		"the header " + begin + " appears in prose only",
		`{"note": "` + begin + `\n"}`,
		"-" + begin + "\n-short\n",
		"  " + begin + "\n  short\n",
	}
	for _, s := range prose {
		assert.NoError(t, g.CheckContent("/r/f", []byte(s)), s)
	}
}

func TestPGPPrivateKey(t *testing.T) {
	g := gate(t, ModeOn)
	begin := "-----BEGIN " + "PGP PRIVATE KEY" + " BLOCK-----"
	body := strings.Repeat("lQdG", 16)
	samples := map[string]string{
		"plain":  begin + "\n\n" + body + "\n",
		"armour": begin + "\nVersion: GnuPG v2\nComment: x\n\n" + body + "\n",
		"yaml":   "key: |\n  " + begin + "\n  \n  " + body + "\n",
		"json":   `{"k": "` + begin + `\n\n` + body + `"}`,
		"diff":   "+" + begin + "\n+\n+" + body + "\n",
	}
	for name, sample := range samples {
		assert.Equal(t, "pgp-private-key", refusalOf(t, g.CheckContent("/r/f", []byte(sample))).Rule, name)
	}
	assert.NoError(t, g.CheckContent("/r/f", []byte("the "+begin+" header appears in prose only")))
	assert.NoError(t, g.CheckContent("/r/f", []byte(begin+"\n\nshort\n")))
}

func TestContentNearMissesAndAlternatives(t *testing.T) {
	g := gate(t, ModeOn)
	refused := map[string]string{
		"aws-access-key-id": "ASIA" + strings.Repeat("Q", 16),
		"github-token":      "github" + "_pat_" + strings.Repeat("a", 60),
	}
	for rule, sample := range refused {
		assert.Equal(t, rule, refusalOf(t, g.CheckContent("/r/f", []byte(sample))).Rule, rule)
	}
	nearMisses := []string{
		"xox" + "b-" + strings.Repeat("1", 9),
		"sk-" + "ant-" + strings.Repeat("a", 10),
		"AI" + "za" + strings.Repeat("b", 30),
		"sk" + "_live_" + strings.Repeat("c", 20),
		"github" + "_pat_" + strings.Repeat("a", 30),
		"ASIA" + strings.Repeat("Q", 15),
	}
	for _, s := range nearMisses {
		assert.NoError(t, g.CheckContent("/r/f", []byte(s)), s)
	}
}

func TestPEMInSourceAndCommentCarriers(t *testing.T) {
	g := gate(t, ModeOn)
	begin, body, end := pemParts()
	samples := map[string]string{
		"hash-comment":  "# " + begin + "\n# " + body + "\n# " + end + "\n",
		"slash-comment": "// " + begin + "\n// " + body + "\n",
		"md-quote":      "> " + begin + "\n> " + body + "\n",
		"go-concat":     `k := "` + begin + `\n" +` + "\n\t" + `"` + body + `\n" +` + "\n\t" + `"` + end + `\n"`,
		"py-list":       `K = ["` + begin + `\n",` + "\n    " + `"` + body + `\n"]`,
		"js-template":   "const k = `" + begin + "\n`+\n`" + body + "`",
		"single-quotes": `k = '` + begin + `\n' + '` + body + `'`,
	}
	for name, sample := range samples {
		assert.Equal(t, "pem-private-key", refusalOf(t, g.CheckContent("/r/f", []byte(sample))).Rule, name)
	}
	prose := []string{
		"# " + begin + "\n# short\n",
		`k := "` + begin + `\n" + "short"`,
		"> the header " + begin + " is named here\n> and more prose follows it\n",
	}
	for _, s := range prose {
		assert.NoError(t, g.CheckContent("/r/f", []byte(s)), s)
	}
}

func b64Armour(kind string) string {
	line := "-----BEGIN " + kind + "-----\n" + strings.Repeat("MIIE", 16) + "\n"
	return base64.StdEncoding.EncodeToString([]byte(line))
}

func TestBase64EncodedPrivateKey(t *testing.T) {
	g := gate(t, ModeOn)
	for _, kind := range []string{"PRIVATE KEY", "RSA PRIVATE KEY", "EC PRIVATE KEY", "DSA PRIVATE KEY", "OPENSSH PRIVATE KEY", "ENCRYPTED PRIVATE KEY"} {
		sample := "data:\n  tls.key: " + b64Armour(kind) + "\n"
		assert.Equal(t, "pem-private-key-base64", refusalOf(t, g.CheckContent("/r/f", []byte(sample))).Rule, kind)
	}
	for _, kind := range []string{"CERTIFICATE", "PUBLIC KEY", "CERTIFICATE REQUEST", "X509 CRL"} {
		sample := "data:\n  tls.crt: " + b64Armour(kind) + "\n"
		assert.NoError(t, g.CheckContent("/r/f", []byte(sample)), kind)
	}
}
