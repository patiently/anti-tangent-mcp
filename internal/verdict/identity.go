package verdict

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
)

// fingerprintHexDigits is how much of the SHA-256 digest a fingerprint keeps.
const fingerprintHexDigits = 8

var (
	keyWhitespaceRe = regexp.MustCompile(`\s+`)
	displayIDRe     = regexp.MustCompile(`^f_[0-9a-f]{8}(-([2-9]|[1-9][0-9]+))?$`)
)

// normalizeKey lowercases s, collapses each run of whitespace to one space,
// and strips surrounding space and trailing '.', ':', ';' and ',', so a
// reviewer restating a criterion with different case, spacing or closing
// punctuation keeps its fingerprint.
func normalizeKey(s string) string {
	s = strings.TrimSpace(keyWhitespaceRe.ReplaceAllString(strings.ToLower(s), " "))
	for {
		t := strings.TrimSpace(strings.TrimRight(s, ".:;,"))
		if t == s {
			return t
		}
		s = t
	}
}

// Fingerprint identifies a finding across rounds: "f_" and the first eight hex
// digits of the SHA-256 of its category, task key and criterion, the last two
// normalized, joined by U+001F. taskKey is empty for the session tools. For a
// validate_plan task finding it is the task title without its "Task N:"
// prefix, because renumbering a plan would otherwise change the fingerprint of
// every task after an insertion.
func Fingerprint(category Category, taskKey, criterion string) string {
	sum := sha256.Sum256([]byte(string(category) + "\x1f" + normalizeKey(taskKey) + "\x1f" + normalizeKey(criterion)))
	return "f_" + hex.EncodeToString(sum[:])[:fingerprintHexDigits]
}

// BaseID returns the fingerprint a display ID was built from, without its
// "-n" suffix. Rulings and repeats match on it: a suffix depends only on the
// order one response emitted its findings in.
func BaseID(id string) string {
	if i := strings.IndexByte(id, '-'); i >= 0 {
		return id[:i]
	}
	return id
}

// ValidDisplayID reports whether id has a display ID's shape: "f_", eight
// lowercase hex digits, and an optional "-n" suffix with n of at least 2.
func ValidDisplayID(id string) bool {
	return displayIDRe.MatchString(id)
}

// IDAssigner hands out display IDs across one response. The first finding
// with a fingerprint gets the bare fingerprint; each later one in the same
// response gets "-2", "-3" and so on, in assignment order.
type IDAssigner struct {
	seen map[string]int
}

// NewIDAssigner returns an assigner for one response.
func NewIDAssigner() *IDAssigner {
	return &IDAssigner{seen: map[string]int{}}
}

func (a *IDAssigner) next(fingerprint string) string {
	a.seen[fingerprint]++
	if n := a.seen[fingerprint]; n > 1 {
		return fingerprint + "-" + strconv.Itoa(n)
	}
	return fingerprint
}

// Assign sets the ID of every finding in fs, in order.
func (a *IDAssigner) Assign(fs []Finding, taskKey string) {
	for i := range fs {
		fs[i].ID = a.next(Fingerprint(fs[i].Category, taskKey, fs[i].Criterion))
	}
}

// AssignWaived sets the ID of every waived entry in ws, in order.
func (a *IDAssigner) AssignWaived(ws []WaivedFinding, taskKey string) {
	for i := range ws {
		ws[i].ID = a.next(Fingerprint(ws[i].Category, taskKey, ws[i].Criterion))
	}
}

// clearServerSetFields empties the fields only the server sets, so a reviewer
// response that carries them cannot choose a finding's identity or mark it a
// repeat. keepSameAs is true for per-task findings, where same_as is the
// reviewer's own field; the plan schemas have none.
func clearServerSetFields(f *Finding, keepSameAs bool) {
	f.ID = ""
	f.RepeatOf = ""
	if !keepSameAs {
		f.SameAs = nil
	}
}
