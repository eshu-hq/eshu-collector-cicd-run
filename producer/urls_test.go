// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"testing"
)

// GitLab project-URL contract: the pipeline web_url's "/-/..." suffix is
// stripped to the project root, falling back to the claim scope's
// gitlab-ci://<host>/<path> encoding. Each vector drives the real
// gitLabProjectURL path used by the run fact and provider repository ID.
func TestGitLabProjectURLFromWebURLStripsResourceSuffix(t *testing.T) {
	t.Parallel()
	vectors := []struct {
		name   string
		webURL string
		want   string
	}{
		{"pipeline url strips to project root", "https://gitlab.example.com/group/project/-/pipelines/123", "https://gitlab.example.com/group/project"},
		{"job url strips to project root", "https://gitlab.example.com/group/sub/project/-/jobs/9", "https://gitlab.example.com/group/sub/project"},
		{"empty stays empty", "", ""},
		{"no resource suffix yields empty", "https://gitlab.example.com/group/project", ""},
		{"suffix at start is not a project url", "/-/pipelines/123", ""},
		{"missing scheme yields empty", "gitlab.example.com/group/project/-/pipelines/1", ""},
		{"surrounding whitespace is trimmed", "  https://gitlab.example.com/group/project/-/pipelines/1  ", "https://gitlab.example.com/group/project"},
	}
	for _, vector := range vectors {
		t.Run(vector.name, func(t *testing.T) {
			t.Parallel()
			if got := gitLabProjectURLFromWebURL(vector.webURL); got != vector.want {
				t.Fatalf("gitLabProjectURLFromWebURL(%q) = %q, want %q", vector.webURL, got, vector.want)
			}
		})
	}
}

func TestGitLabProjectURLFromScopeIDReconstructsHTTPSURL(t *testing.T) {
	t.Parallel()
	vectors := []struct {
		name    string
		scopeID string
		want    string
	}{
		{"scope id becomes https url", "gitlab-ci://gitlab.example.com/group/project", "https://gitlab.example.com/group/project"},
		{"nested path is kept", "gitlab-ci://gitlab.example.com/group/sub/project", "https://gitlab.example.com/group/sub/project"},
		{"empty stays empty", "", ""},
		{"wrong scheme yields empty", "github://gitlab.example.com/group/project", ""},
		{"bare prefix yields empty", "gitlab-ci://", ""},
		{"prefix alone yields empty", "gitlab-ci:///", ""},
		{"surrounding whitespace is trimmed", "  gitlab-ci://gitlab.example.com/group/project  ", "https://gitlab.example.com/group/project"},
	}
	for _, vector := range vectors {
		t.Run(vector.name, func(t *testing.T) {
			t.Parallel()
			if got := gitLabProjectURLFromScopeID(vector.scopeID); got != vector.want {
				t.Fatalf("gitLabProjectURLFromScopeID(%q) = %q, want %q", vector.scopeID, got, vector.want)
			}
		})
	}
}

func TestGitLabProjectURLPrefersWebURLThenScope(t *testing.T) {
	t.Parallel()
	got := gitLabProjectURL(
		"gitlab-ci://gitlab.example.com/group/project",
		"https://gitlab.example.com/group/project/-/pipelines/7",
	)
	if got != "https://gitlab.example.com/group/project" {
		t.Fatalf("gitLabProjectURL(web) = %q, want project root", got)
	}
	got = gitLabProjectURL("gitlab-ci://gitlab.example.com/group/project", "not-a-url")
	if got != "https://gitlab.example.com/group/project" {
		t.Fatalf("gitLabProjectURL(scope fallback) = %q, want scope url", got)
	}
	if got := gitLabProjectURL("", ""); got != "" {
		t.Fatalf("gitLabProjectURL(empty) = %q, want empty", got)
	}
}
