// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// maxDeploymentStatuses bounds the status window fetched per deployment.
// GitHub deployment statuses are append-only and a deployment typically
// carries only a few transitions; a truncated window emits a
// deployment_statuses_truncated warning rather than being silently short.
// It ports in-tree maxDeploymentStatusesPerDeployment.
const maxDeploymentStatuses = 20

// GitHubClient fetches bounded GitHub Actions metadata through GitHub's
// REST API. It ports ghactionsruntime.GitHubClient (runs plus bounded jobs
// and artifacts per run, deployments plus bounded statuses) into the
// satellite, normalizing into the producer.Evidence seam instead of the
// in-tree RunSnapshot maps: the emission builders consume the result
// unchanged.
type GitHubClient struct {
	// HTTPClient overrides the fetch transport (nil means a bounded
	// 30-second client). It exists as the fake-server test seam.
	HTTPClient *http.Client
}

// FetchWindow returns the target repository's most recent runs (bounded by
// MaxRuns) with bounded jobs, artifacts, and deployments, normalized to
// one Evidence per run. Outcomes follow the SHIM_MAP table: a committed
// prefix with truncation or throttling signals partial (the flags ride the
// Window for the composer); a first-request throttle, transport fault, or
// malformed payload is retryable; auth refusals and missing resources are
// terminal invalid_config sub-classes. An empty window is not an error: it
// composes to an unchanged generation with no facts.
func (c GitHubClient) FetchWindow(ctx context.Context, target ResolvedTarget, token string) (Window, error) {
	if err := ctx.Err(); err != nil {
		return Window{}, &RetryableError{Cause: err}
	}
	if strings.TrimSpace(token) == "" {
		return Window{}, &TerminalError{Reason: ReasonActivation, Cause: errors.New("github fetch requires a claim credential")}
	}
	runs, totalCount, err := c.fetchRunPage(ctx, target, token)
	if err != nil {
		return Window{}, classifyFirstError(err)
	}
	window := Window{
		Provider: ProviderGitHubActions,
		// effectiveBound, not the raw configured value: a zero MaxRuns
		// fetches the provider default page, so the full-page fallback
		// must compare against that page, never against zero (which
		// would mark every empty window truncated).
		Truncated: runsPageTruncated(totalCount, len(runs), effectiveBound(target.MaxRuns, 10)),
	}
	for _, run := range runs {
		// halt arrives with a nil error: the fetched prefix commits
		// partial and the window stops here (further reads would
		// throttle too). A halt with nothing committed releases
		// retryable instead of reporting an unchanged success while
		// the provider is throttling.
		evidence, halt, err := c.fetchOne(ctx, target, token, run)
		if err != nil {
			return Window{}, err
		}
		if halt != nil {
			window.RateLimited = true
			if strings.TrimSpace(evidence.RunID) == "" {
				return Window{}, &RetryableError{
					Cause:      errors.New("provider rate limited before any run committed"),
					RetryAfter: halt.retryAfter,
				}
			}
			window.Evidences = append(window.Evidences, evidence)
			break
		}
		window.Evidences = append(window.Evidences, evidence)
	}
	if !window.RateLimited && len(window.Evidences) > 0 {
		if err := c.fetchDeploymentsInto(ctx, target, token, &window); err != nil {
			var limited RateLimitError
			if errors.As(err, &limited) {
				window.RateLimited = true
			} else {
				return Window{}, err
			}
		}
	}
	return window, nil
}

// classifyFirstError maps a runs-list failure before anything committed: a
// first-request throttle is retryable with the provider's guidance, an HTTP
// refusal terminal-fails, anything else stays retryable.
func classifyFirstError(err error) error {
	var limited RateLimitError
	if errors.As(err, &limited) {
		return &RetryableError{Cause: err, RetryAfter: limited.RetryAfter}
	}
	return classifyHTTPError(err)
}

// fetchHalt stops a window with a partial commit: the fetched prefix
// still composes (partial + rate_limited warning) when it holds at
// least one run, and releases retryable otherwise.
type fetchHalt struct {
	retryAfter time.Duration
}

// fetchOne fetches one run's bounded jobs and artifacts and normalizes the
// run to Evidence. A throttled sub-page returns a halt with the run
// committed partial; any other failure aborts the fetch.
func (c GitHubClient) fetchOne(ctx context.Context, target ResolvedTarget, token string, run map[string]any) (Evidence, *fetchHalt, error) {
	evidence, err := normalizeGitHubRun(run)
	if err != nil {
		return Evidence{}, nil, &RetryableError{Cause: err}
	}
	jobs, jobsPartial, err := c.fetchJobs(ctx, target, token, evidence.RunID)
	if err != nil {
		var limited RateLimitError
		if errors.As(err, &limited) {
			evidence.JobsPartial = true
			return evidence, &fetchHalt{retryAfter: limited.RetryAfter}, nil
		}
		return Evidence{}, nil, classifyHTTPError(err)
	}
	artifacts, artifactsPartial, err := c.fetchArtifacts(ctx, target, token, evidence.RunID)
	if err != nil {
		var limited RateLimitError
		if errors.As(err, &limited) {
			normalizeGitHubJobs(&evidence, jobs, jobsPartial)
			evidence.ArtifactsPartial = true
			return evidence, &fetchHalt{retryAfter: limited.RetryAfter}, nil
		}
		return Evidence{}, nil, classifyHTTPError(err)
	}
	normalizeGitHubJobs(&evidence, jobs, jobsPartial)
	normalizeGitHubArtifacts(&evidence, run, artifacts, artifactsPartial)
	return evidence, nil, nil
}

// fetchDeploymentsInto fetches the bounded deployments window and attaches
// its normalized rows plus collection warnings to the first evidence. A
// 403/404 refusal is the documented optional-permission shape (Deployments
// read is a separate GitHub permission from Actions read): run facts stay
// committed with an explicit permission warning instead of dropping the
// generation.
func (c GitHubClient) fetchDeploymentsInto(ctx context.Context, target ResolvedTarget, token string, window *Window) error {
	deployments, truncated, err := c.fetchDeploymentPage(ctx, target, token)
	if err != nil {
		var httpErr HTTPError
		if errors.As(err, &httpErr) && (httpErr.StatusCode == http.StatusForbidden || httpErr.StatusCode == http.StatusNotFound) {
			window.Evidences[0].Warnings = append(window.Evidences[0].Warnings, WarningEvidence{
				Key:     "deployments:permission_denied",
				Reason:  "deployment_permission_denied",
				Message: "deployments read refused; run, job and artifact facts were still collected",
			})
			return nil
		}
		return classifyHTTPError(err)
	}
	rows := make([]DeploymentEvidence, 0, len(deployments))
	for index, deployment := range deployments {
		deploymentID, err := numericProviderID(deployment["id"])
		if err != nil || strings.TrimSpace(deploymentID) == "" {
			return &RetryableError{Cause: fmt.Errorf("github deployment.id: %v", deployment["id"])}
		}
		if anyString(deployment["sha"]) == "" || anyString(deployment["environment"]) == "" {
			window.Evidences[0].Warnings = append(window.Evidences[0].Warnings, WarningEvidence{
				Key:     fmt.Sprintf("deployment:%d", index),
				Reason:  "deployment_missing_required_field",
				Message: "deployment metadata omitted provider id, sha, or environment",
			})
			continue
		}
		statuses, statusesPartial, err := c.fetchDeploymentStatuses(ctx, target, token, deploymentID)
		if err != nil {
			var httpErr HTTPError
			if errors.As(err, &httpErr) && (httpErr.StatusCode == http.StatusForbidden || httpErr.StatusCode == http.StatusNotFound) {
				window.Evidences[0].Warnings = append(window.Evidences[0].Warnings, WarningEvidence{
					Key:     "deployments:permission_denied",
					Reason:  "deployment_permission_denied",
					Message: "deployments read refused; run, job and artifact facts were still collected",
				})
				return nil
			}
			return classifyHTTPError(err)
		}
		normalized, err := normalizeGitHubDeployment(deployment, statuses)
		if err != nil {
			return &RetryableError{Cause: err}
		}
		rows = append(rows, normalized...)
		if statusesPartial {
			window.Evidences[0].Warnings = append(window.Evidences[0].Warnings, WarningEvidence{
				Key:     "deployment:" + deploymentID + ":statuses:partial",
				Reason:  "deployment_statuses_truncated",
				Message: "deployment status window was truncated; a later transition may be missing",
			})
		}
	}
	window.Evidences[0].Deployments = rows
	if truncated {
		window.Evidences[0].Warnings = append(window.Evidences[0].Warnings, WarningEvidence{
			Key:     "deployments:partial",
			Reason:  "deployments_truncated",
			Message: "additional deployments exist beyond the collected window",
		})
	}
	return nil
}

// runsPageTruncated reports whether more workflow runs exist beyond the
// fetched window. It ports the in-tree heuristic: the provider's exact
// total_count wins when present, otherwise a full page implies more.
func runsPageTruncated(totalCount, fetchedLen, maxRuns int) bool {
	if totalCount > 0 {
		return totalCount > fetchedLen
	}
	return fetchedLen == maxRuns
}

// effectiveBound resolves a fetch per_page: a non-positive configured
// bound selects the caller default (the provider's own page default);
// positive values pass through (caps are enforced at config load).
func effectiveBound(raw, def int) int {
	if raw <= 0 {
		return def
	}
	return raw
}

func (c GitHubClient) fetchRunPage(ctx context.Context, target ResolvedTarget, token string) ([]map[string]any, int, error) {
	endpoint, err := joinURL("api_base_url", target.APIBaseURL,
		"/repos/"+target.Repository+"/actions/runs",
		map[string]string{"per_page": strconv.Itoa(effectiveBound(target.MaxRuns, 10))})
	if err != nil {
		return nil, 0, &TerminalError{Reason: ReasonActivation, Cause: err}
	}
	var decoded struct {
		TotalCount   int              `json:"total_count"`
		WorkflowRuns []map[string]any `json:"workflow_runs"`
	}
	if err := c.getJSON(ctx, token, endpoint, &decoded); err != nil {
		return nil, 0, fmt.Errorf("fetch github actions workflow runs: %w", err)
	}
	return decoded.WorkflowRuns, decoded.TotalCount, nil
}

func (c GitHubClient) fetchJobs(ctx context.Context, target ResolvedTarget, token, runID string) ([]map[string]any, bool, error) {
	endpoint, err := joinURL("api_base_url", target.APIBaseURL,
		"/repos/"+target.Repository+"/actions/runs/"+runID+"/jobs",
		map[string]string{"per_page": strconv.Itoa(effectiveBound(target.MaxJobs, 30))})
	if err != nil {
		return nil, false, &TerminalError{Reason: ReasonActivation, Cause: err}
	}
	var decoded struct {
		TotalCount int              `json:"total_count"`
		Jobs       []map[string]any `json:"jobs"`
	}
	if err := c.getJSON(ctx, token, endpoint, &decoded); err != nil {
		return nil, false, fmt.Errorf("fetch github actions jobs: %w", err)
	}
	return decoded.Jobs, decoded.TotalCount > len(decoded.Jobs), nil
}

func (c GitHubClient) fetchArtifacts(ctx context.Context, target ResolvedTarget, token, runID string) ([]map[string]any, bool, error) {
	endpoint, err := joinURL("api_base_url", target.APIBaseURL,
		"/repos/"+target.Repository+"/actions/runs/"+runID+"/artifacts",
		map[string]string{"per_page": strconv.Itoa(effectiveBound(target.MaxArtifacts, 30))})
	if err != nil {
		return nil, false, &TerminalError{Reason: ReasonActivation, Cause: err}
	}
	var decoded struct {
		TotalCount int              `json:"total_count"`
		Artifacts  []map[string]any `json:"artifacts"`
	}
	if err := c.getJSON(ctx, token, endpoint, &decoded); err != nil {
		return nil, false, fmt.Errorf("fetch github actions artifacts: %w", err)
	}
	return decoded.Artifacts, decoded.TotalCount > len(decoded.Artifacts), nil
}

func (c GitHubClient) fetchDeploymentPage(ctx context.Context, target ResolvedTarget, token string) ([]map[string]any, bool, error) {
	bound := effectiveBound(target.MaxDeployments, 10)
	endpoint, err := joinURL("api_base_url", target.APIBaseURL,
		"/repos/"+target.Repository+"/deployments",
		map[string]string{"per_page": strconv.Itoa(bound)})
	if err != nil {
		return nil, false, &TerminalError{Reason: ReasonActivation, Cause: err}
	}
	var deployments []map[string]any
	if err := c.getJSON(ctx, token, endpoint, &deployments); err != nil {
		return nil, false, fmt.Errorf("fetch github deployments: %w", err)
	}
	return deployments, len(deployments) == bound, nil
}

func (c GitHubClient) fetchDeploymentStatuses(ctx context.Context, target ResolvedTarget, token, deploymentID string) ([]map[string]any, bool, error) {
	endpoint, err := joinURL("api_base_url", target.APIBaseURL,
		"/repos/"+target.Repository+"/deployments/"+deploymentID+"/statuses",
		map[string]string{"per_page": strconv.Itoa(maxDeploymentStatuses)})
	if err != nil {
		return nil, false, &TerminalError{Reason: ReasonActivation, Cause: err}
	}
	var statuses []map[string]any
	if err := c.getJSON(ctx, token, endpoint, &statuses); err != nil {
		return nil, false, fmt.Errorf("fetch github deployment statuses: %w", err)
	}
	return statuses, len(statuses) == maxDeploymentStatuses, nil
}

func (c GitHubClient) getJSON(ctx context.Context, token, endpoint string, out any) error {
	client := c.HTTPClient
	if client == nil {
		client = defaultHTTPClient()
	}
	return getJSON(ctx, client, "github_actions", endpoint, func(r *http.Request) {
		r.Header.Set("Accept", "application/vnd.github+json")
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}, out, githubRateLimited, githubRetryAfter)
}

func githubRateLimited(response *http.Response) bool {
	if response.StatusCode == http.StatusTooManyRequests {
		return true
	}
	if response.StatusCode != http.StatusForbidden {
		return false
	}
	return strings.TrimSpace(response.Header.Get("X-RateLimit-Remaining")) == "0" ||
		strings.TrimSpace(response.Header.Get("Retry-After")) != ""
}

func githubRetryAfter(header http.Header, now time.Time) time.Duration {
	if retryAfter := parseRetryAfterHeader(header.Get("Retry-After"), now); retryAfter > 0 {
		return retryAfter
	}
	if epoch, err := strconv.ParseInt(strings.TrimSpace(header.Get("X-RateLimit-Reset")), 10, 64); err == nil && epoch > 0 {
		if delay := time.Unix(epoch, 0).UTC().Sub(now); delay > 0 {
			return delay
		}
	}
	return 0
}
