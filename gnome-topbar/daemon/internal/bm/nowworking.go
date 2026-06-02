package bm

import (
	"strings"
	"time"
)

type NowWorking struct {
	Body       string    `json:"body"`
	Updated    time.Time `json:"updated"`
	HasUpdated bool      `json:"has_updated"`
	NotFound   bool      `json:"not_found"`
}

// ParseNowWorking splits optional YAML frontmatter from the body and reads an
// `updated:` RFC3339 timestamp if present.
func ParseNowWorking(md string) NowWorking {
	var nw NowWorking
	body := md
	if strings.HasPrefix(md, "---\n") {
		if end := strings.Index(md[4:], "\n---"); end >= 0 {
			front := md[4 : 4+end]
			rest := md[4+end+len("\n---"):]
			rest = strings.TrimPrefix(rest, "\n")
			body = rest
			for _, line := range strings.Split(front, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "updated:") {
					v := strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
					if t, err := time.Parse(time.RFC3339, v); err == nil {
						nw.Updated = t
						nw.HasUpdated = true
					}
				}
			}
		}
	}
	nw.Body = strings.TrimSpace(body)
	if strings.HasPrefix(strings.TrimSpace(md), "# Note Not Found in") {
		nw.NotFound = true
		nw.Body = ""
	}
	return nw
}
