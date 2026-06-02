// Package github fetches the operator's open PRs and review-requested PRs via
// the GitHub search API, reusing gh CLI credentials through go-gh in prod.
package github

import (
	"net/url"
	"strings"
	"time"
)

type PR struct {
	Repo      string    `json:"repo"`
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	Author    string    `json:"author"`
	UpdatedAt time.Time `json:"updated_at"`
}

type restGetter interface {
	Get(path string, resp any) error
}

type Source struct{ rest restGetter }

func New(rest restGetter) *Source { return &Source{rest: rest} }

func (s *Source) FetchAuthored() ([]PR, error) {
	return s.search("is:open is:pr author:@me")
}

func (s *Source) FetchReviewRequested() ([]PR, error) {
	return s.search("is:open is:pr review-requested:@me")
}

type searchItem struct {
	Number        int       `json:"number"`
	Title         string    `json:"title"`
	HTMLURL       string    `json:"html_url"`
	RepositoryURL string    `json:"repository_url"`
	UpdatedAt     time.Time `json:"updated_at"`
	User          struct {
		Login string `json:"login"`
	} `json:"user"`
}

func (s *Source) search(q string) ([]PR, error) {
	// per_page=100 (the API max) so a single page covers realistic PR counts;
	// the default page size is only 30, which would silently drop results.
	path := "search/issues?q=" + url.QueryEscape(q) + "&per_page=100"
	var resp struct {
		Items []searchItem `json:"items"`
	}
	if err := s.rest.Get(path, &resp); err != nil {
		return nil, err
	}
	out := make([]PR, 0, len(resp.Items))
	for _, it := range resp.Items {
		out = append(out, PR{
			Repo:      repoFromURL(it.RepositoryURL),
			Number:    it.Number,
			Title:     it.Title,
			URL:       it.HTMLURL,
			Author:    it.User.Login,
			UpdatedAt: it.UpdatedAt,
		})
	}
	return out, nil
}

func repoFromURL(repoURL string) string {
	const marker = "/repos/"
	if i := strings.Index(repoURL, marker); i >= 0 {
		return repoURL[i+len(marker):]
	}
	return repoURL
}

// Note: the prod REST client is constructed in main via
// api.DefaultRESTClient() from github.com/cli/go-gh/v2/pkg/api, whose
// *RESTClient satisfies restGetter (it has Get(path string, resp any) error).
