// Package github provides a minimal GitHub REST API client for PR management.
// It uses only the Go standard library — no external SDK dependency.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const apiBase = "https://api.github.com"

// Client is a GitHub API client scoped to a single owner (user or org).
type Client struct {
	token string
	owner string
	http  *http.Client
}

// NewClient returns a Client authenticated with token and scoped to owner.
func NewClient(token, owner string) *Client {
	return &Client{token: token, owner: owner, http: &http.Client{}}
}

// CreatePR opens a pull request on GitHub.
// Returns the PR number and HTML URL on success.
// When draft is true the PR is opened as a draft.
func (c *Client) CreatePR(ctx context.Context, repo, title, head, base, body string, draft bool) (prNumber int, htmlURL string, err error) {
	payload := map[string]any{
		"title": title,
		"head":  head,
		"base":  base,
		"body":  body,
		"draft": draft,
	}
	var result struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	path := fmt.Sprintf("/repos/%s/%s/pulls", c.owner, repo)
	if err := c.do(ctx, http.MethodPost, path, payload, &result); err != nil {
		return 0, "", err
	}
	return result.Number, result.HTMLURL, nil
}

// UpdatePR patches an existing PR's body text.
// draft is passed through — set it to false to keep the PR in its current
// draft state unchanged (GitHub ignores a no-op draft:false on a non-draft PR).
func (c *Client) UpdatePR(ctx context.Context, repo string, number int, body string, draft bool) error {
	payload := map[string]any{
		"body":  body,
		"draft": draft,
	}
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", c.owner, repo, number)
	return c.do(ctx, http.MethodPatch, path, payload, nil)
}

// PromotePR converts a draft PR to ready-for-review.
// It only sends draft:false so the existing body is preserved.
func (c *Client) PromotePR(ctx context.Context, repo string, number int) error {
	payload := map[string]any{"draft": false}
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", c.owner, repo, number)
	return c.do(ctx, http.MethodPatch, path, payload, nil)
}

// do executes an authenticated GitHub API request and decodes the response
// into out (may be nil to discard the response body).
func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("github: marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, apiBase+path, bodyReader)
	if err != nil {
		return fmt.Errorf("github: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("github: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("github: %s %s: status %d: %s", method, path, resp.StatusCode, respBody)
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("github: decode response: %w", err)
		}
	}
	return nil
}
