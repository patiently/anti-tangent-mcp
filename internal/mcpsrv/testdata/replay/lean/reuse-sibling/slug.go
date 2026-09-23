package feed

import (
	"strings"
	"unicode"
)

// Slugify lower-cases s and joins its runs of letters and digits with single
// hyphens, dropping everything else.
func Slugify(s string) string {
	var b strings.Builder
	gap := false
	for _, r := range strings.ToLower(s) {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			gap = true
			continue
		}
		if gap && b.Len() > 0 {
			b.WriteByte('-')
		}
		b.WriteRune(r)
		gap = false
	}
	return b.String()
}
