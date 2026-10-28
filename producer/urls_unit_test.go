// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"testing"
)

func TestRepositoryHostFallsBackToGitHub(t *testing.T) {
	t.Parallel()

	ctx := emissionContext{claim: validSDKClaim("", "gen-host-1")}
	if got := repositoryHost(ctx, ProviderGitHubActions, Evidence{}); got != "github.com" {
		t.Errorf("repositoryHost(no URLs) = %q, want the github.com fallback", got)
	}
}

func TestCanonicalProviderHostMatrix(t *testing.T) {
	t.Parallel()

	if got := canonicalProviderHost(ProviderGitLabCI, "gitlab.example.com"); got != "gitlab.example.com" {
		t.Errorf("canonicalProviderHost(gitlab) = %q, want the host untouched", got)
	}
	if got := canonicalProviderHost(ProviderGitHubActions, "api.github.com"); got != "github.com" {
		t.Errorf("canonicalProviderHost(api.github.com) = %q, want github.com", got)
	}
	if got := canonicalProviderHost(ProviderGitHubActions, "api.tenant.ghe.com"); got != "tenant.ghe.com" {
		t.Errorf("canonicalProviderHost(ghe api) = %q, want the stripped tenant host", got)
	}
	if got := canonicalProviderHost(ProviderGitHubActions, "ghe.example.com:8443"); got != "ghe.example.com" {
		t.Errorf("canonicalProviderHost(port) = %q, want the bare hostname", got)
	}
}

func TestParseURLHostPathMatrix(t *testing.T) {
	t.Parallel()

	if _, err := parseURLHostPath("://bad"); err == nil {
		t.Error("parseURLHostPath(unparseable) = nil, want rejection")
	}
	if _, err := parseURLHostPath("https://"); err == nil {
		t.Error("parseURLHostPath(no host) = nil, want errEmptyHost")
	}
	got, err := parseURLHostPath("https://gitlab.example.com/group/project")
	if err != nil {
		t.Fatalf("parseURLHostPath() error = %v, want nil", err)
	}
	if got != "gitlab.example.com/group/project" {
		t.Errorf("parseURLHostPath() = %q, want host plus path", got)
	}
}

func TestProviderRepositoryIDMatrix(t *testing.T) {
	t.Parallel()

	gitlabCtx := emissionContext{claim: validSDKClaim("other-scope", "gen-repo-1")}
	if got := providerRepositoryID(gitlabCtx, ProviderGitLabCI, Evidence{}); got != "" {
		t.Errorf("providerRepositoryID(gitlab, no URL) = %q, want empty", got)
	}
	badScopeCtx := emissionContext{claim: validSDKClaim("gitlab-ci://[::1", "gen-repo-2")}
	if got := providerRepositoryID(badScopeCtx, ProviderGitLabCI, Evidence{}); got != "" {
		t.Errorf("providerRepositoryID(gitlab, bad scope URL) = %q, want empty", got)
	}
	githubCtx := emissionContext{claim: validSDKClaim(windowScope(), "gen-repo-3")}
	if got := providerRepositoryID(githubCtx, ProviderGitHubActions, Evidence{}); got != "" {
		t.Errorf("providerRepositoryID(github, no full name) = %q, want empty", got)
	}
}
