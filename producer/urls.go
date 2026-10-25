// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"errors"
	"net/url"
	"strings"
)

// errEmptyHost marks a project URL with no host.
var errEmptyHost = errors.New("project url has no host")

// repositoryHost resolves the provider repository host for provenance,
// trying the repository URL, the target SourceURI, then the claim scope.
// It mirrors the in-tree repositoryHost fallback chain.
func repositoryHost(ctx emissionContext, provider Provider, evidence Evidence) string {
	repository := evidence.Repository
	for _, rawURL := range []string{repository.HTMLURL, repository.WebURL, ctx.sourceURI, ctx.claim.Scope.ID} {
		if parsed, err := url.Parse(strings.TrimSpace(rawURL)); err == nil && parsed.Host != "" {
			return canonicalProviderHost(provider, parsed.Host)
		}
	}
	return "github.com"
}

// canonicalProviderHost maps the api. subdomain prefix to the canonical
// host for github.com and GitHub Enterprise Cloud tenants only, mirroring
// the in-tree canonicalGitHubHost. The mapping is narrow by design: an
// unconditional prefix strip would silently break the backbone join for
// enterprise hosts whose git host legitimately starts with "api.".
func canonicalProviderHost(provider Provider, host string) string {
	if provider != ProviderGitHubActions {
		return host
	}
	hostname, _, _ := strings.Cut(host, ":")
	lower := strings.ToLower(hostname)
	switch {
	case lower == "api.github.com":
		return "github.com"
	case strings.HasSuffix(lower, ".ghe.com") && strings.HasPrefix(lower, "api."):
		return lower[len("api."):]
	default:
		return hostname
	}
}

// gitLabProjectURL returns the project root URL for GitLab evidence:
// the pipeline web_url with GitLab's "/-/..." sub-resource suffix
// stripped, falling back to the claim scope's gitlab-ci://<host>/<path>
// encoding. It mirrors gitlabRepositoryCanonicalURL without the
// core-owned canonical-ID derivation.
func gitLabProjectURL(scopeID, webURL string) string {
	if projectURL := gitLabProjectURLFromWebURL(webURL); projectURL != "" {
		return projectURL
	}
	return gitLabProjectURLFromScopeID(scopeID)
}

// gitLabProjectURLFromWebURL strips GitLab's "/-/<resource>" suffix from a
// pipeline or job web_url, leaving the project root URL. GitLab always
// renders pipeline pages under the project root at "/-/pipelines/<id>",
// so the prefix before "/-/" is always the project's canonical URL.
func gitLabProjectURLFromWebURL(webURL string) string {
	trimmed := strings.TrimSpace(webURL)
	if trimmed == "" {
		return ""
	}
	idx := strings.Index(trimmed, "/-/")
	if idx <= 0 {
		return ""
	}
	projectURL := trimmed[:idx]
	parsed, err := url.Parse(projectURL)
	if err != nil || parsed.Host == "" || parsed.Scheme == "" {
		return ""
	}
	return projectURL
}

// gitLabProjectURLFromScopeID reconstructs the project's HTTPS URL from a
// "gitlab-ci://<host>/<path>" scope ID.
func gitLabProjectURLFromScopeID(scopeID string) string {
	const prefix = "gitlab-ci://"
	trimmed := strings.TrimSpace(scopeID)
	if !strings.HasPrefix(trimmed, prefix) {
		return ""
	}
	rest := strings.Trim(strings.TrimPrefix(trimmed, prefix), "/")
	if rest == "" {
		return ""
	}
	return "https://" + rest
}

// parseURLHostPath returns the "host/path" locator for a project URL.
func parseURLHostPath(projectURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(projectURL))
	if err != nil {
		return "", err
	}
	if parsed.Host == "" {
		return "", errEmptyHost
	}
	return parsed.Host + parsed.Path, nil
}

// providerRepositoryID returns the raw provider-level repository locator
// (for example "github.com/example-org/example-repo"), preserved as
// provenance alongside the canonical repository_id. It mirrors the
// in-tree helper without the core-owned canonical derivation; see the
// repository_id gap note on pipelineDefinitionFact.
func providerRepositoryID(ctx emissionContext, provider Provider, evidence Evidence) string {
	switch provider {
	case ProviderGitLabCI:
		projectURL := gitLabProjectURL(ctx.claim.Scope.ID, evidence.Repository.WebURL)
		if projectURL == "" {
			return ""
		}
		parsed, err := parseURLHostPath(projectURL)
		if err != nil {
			return ""
		}
		return parsed
	default:
		repository := evidence.Repository
		fullName := strings.Trim(strings.TrimSpace(repository.FullName), "/")
		if fullName == "" {
			return ""
		}
		return repositoryHost(ctx, provider, evidence) + "/" + fullName
	}
}
