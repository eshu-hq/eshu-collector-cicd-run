// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// GitLabClient fetches bounded GitLab CI/CD pipeline metadata through
// GitLab's REST API. It ports gitlabciruntime.GitLabClient (pipeline list
// plus detail plus bounded jobs per pipeline; job artifacts ride inline)
// into the satellite, normalizing into the producer.Evidence seam instead
// of the in-tree PipelineSnapshot maps.
type GitLabClient struct {
	// HTTPClient overrides the fetch transport (nil means a bounded
	// 30-second client). It exists as the fake-server test seam.
	HTTPClient *http.Client
}

// FetchWindow returns the target project's most recent pipelines (bounded
// by MaxRuns) with bounded jobs each, normalized to one Evidence per
// pipeline. Outcomes follow the SHIM_MAP table like the GitHub fetch: a
// committed prefix with truncation or throttling signals partial; a
// first-request throttle, transport fault, or malformed payload is
// retryable; refusals and missing projects are terminal
// invalid_config sub-classes. An empty window composes to unchanged.
func (c GitLabClient) FetchWindow(ctx context.Context, target ResolvedTarget, token string) (Window, error) {
	if err := ctx.Err(); err != nil {
		return Window{}, &RetryableError{Cause: err}
	}
	if strings.TrimSpace(token) == "" {
		return Window{}, &TerminalError{Reason: ReasonActivation, Cause: errors.New("gitlab fetch requires a claim credential")}
	}
	pipelines, total, err := c.fetchPipelineList(ctx, target, token)
	if err != nil {
		return Window{}, classifyFirstError(err)
	}
	window := Window{
		Provider: ProviderGitLabCI,
		// effectiveBound, not the raw configured value: a zero MaxRuns
		// fetches the provider default page, so the full-page fallback
		// must compare against that page, never against zero (which
		// would mark every empty window truncated).
		Truncated: pipelinesPageTruncated(total, len(pipelines), effectiveBound(target.MaxRuns, 10)),
	}
	for _, slim := range pipelines {
		// A halt with nothing committed (a throttled detail read on
		// the first pipeline) releases retryable with the
		// provider's guidance; otherwise the prefix commits partial.
		evidence, halt, err := c.fetchOne(ctx, target, token, slim)
		if err != nil {
			return Window{}, err
		}
		if halt != nil {
			window.RateLimited = true
			if strings.TrimSpace(evidence.RunID) == "" {
				return Window{}, &RetryableError{
					Cause:      errors.New("provider rate limited before any pipeline committed"),
					RetryAfter: halt.retryAfter,
				}
			}
			window.Evidences = append(window.Evidences, evidence)
			break
		}
		window.Evidences = append(window.Evidences, evidence)
	}
	return window, nil
}

// fetchOne fetches one pipeline's detail plus its bounded jobs and
// normalizes the pipeline to Evidence. A throttled detail or jobs page
// returns a halt: the jobs halt carries the pipeline committed partial,
// the detail halt carries none (earlier pipelines still commit).
// Any other failure aborts the fetch.
func (c GitLabClient) fetchOne(ctx context.Context, target ResolvedTarget, token string, slim map[string]any) (Evidence, *fetchHalt, error) {
	pipelineID, err := numericProviderID(slim["id"])
	if err != nil || strings.TrimSpace(pipelineID) == "" {
		return Evidence{}, nil, &RetryableError{Cause: fmt.Errorf("gitlab ci pipeline.id: %v", slim["id"])}
	}
	detail, err := c.fetchPipelineDetail(ctx, target, token, pipelineID)
	if err != nil {
		var limited RateLimitError
		if errors.As(err, &limited) {
			return Evidence{}, &fetchHalt{retryAfter: limited.RetryAfter}, nil
		}
		return Evidence{}, nil, classifyHTTPError(err)
	}
	evidence, err := normalizeGitLabPipeline(detail)
	if err != nil {
		return Evidence{}, nil, &RetryableError{Cause: err}
	}
	jobs, jobsPartial, err := c.fetchJobs(ctx, target, token, pipelineID)
	if err != nil {
		var limited RateLimitError
		if errors.As(err, &limited) {
			evidence.JobsPartial = true
			return evidence, &fetchHalt{retryAfter: limited.RetryAfter}, nil
		}
		return Evidence{}, nil, classifyHTTPError(err)
	}
	normalizeGitLabJobs(&evidence, jobs, jobsPartial)
	return evidence, nil, nil
}

// pipelinesPageTruncated ports the in-tree heuristic: GitLab's exact
// X-Total wins when present, otherwise a full page implies more. GitLab
// documents that X-Total "may not be returned", so the fallback is
// required, not defensive-only.
func pipelinesPageTruncated(total, fetchedLen, maxRuns int) bool {
	if total > 0 {
		return total > fetchedLen
	}
	return fetchedLen == maxRuns
}

func (c GitLabClient) fetchPipelineList(ctx context.Context, target ResolvedTarget, token string) ([]map[string]any, int, error) {
	endpoint, err := joinURL("api_base_url", target.APIBaseURL,
		"/projects/"+url.PathEscape(target.Repository)+"/pipelines",
		map[string]string{
			"per_page": strconv.Itoa(effectiveBound(target.MaxRuns, 10)),
			"order_by": "id",
			"sort":     "desc",
		})
	if err != nil {
		return nil, 0, &TerminalError{Reason: ReasonActivation, Cause: err}
	}
	var decoded []map[string]any
	total, err := c.getJSON(ctx, token, endpoint, &decoded)
	if err != nil {
		return nil, 0, fmt.Errorf("fetch gitlab ci pipelines: %w", err)
	}
	return decoded, total, nil
}

func (c GitLabClient) fetchPipelineDetail(ctx context.Context, target ResolvedTarget, token, pipelineID string) (map[string]any, error) {
	endpoint, err := joinURL("api_base_url", target.APIBaseURL,
		"/projects/"+url.PathEscape(target.Repository)+"/pipelines/"+url.PathEscape(pipelineID), nil)
	if err != nil {
		return nil, &TerminalError{Reason: ReasonActivation, Cause: err}
	}
	var decoded map[string]any
	if _, err := c.getJSON(ctx, token, endpoint, &decoded); err != nil {
		return nil, fmt.Errorf("fetch gitlab ci pipeline %s: %w", pipelineID, err)
	}
	return decoded, nil
}

func (c GitLabClient) fetchJobs(ctx context.Context, target ResolvedTarget, token, pipelineID string) ([]map[string]any, bool, error) {
	endpoint, err := joinURL("api_base_url", target.APIBaseURL,
		"/projects/"+url.PathEscape(target.Repository)+"/pipelines/"+url.PathEscape(pipelineID)+"/jobs",
		map[string]string{"per_page": strconv.Itoa(effectiveBound(target.MaxJobs, 30))})
	if err != nil {
		return nil, false, &TerminalError{Reason: ReasonActivation, Cause: err}
	}
	var decoded []map[string]any
	total, err := c.getJSON(ctx, token, endpoint, &decoded)
	if err != nil {
		return nil, false, fmt.Errorf("fetch gitlab ci jobs: %w", err)
	}
	return decoded, total > len(decoded), nil
}

// getJSON issues one authenticated GET, decodes the JSON body, and
// returns GitLab's X-Total pagination value (0 when absent or
// unparseable, per pipelinesPageTruncated's contract). It mirrors the
// in-tree getJSONWithTotal: GitLab pagination rides a header, not a body
// wrapper, so the shared body-only helper cannot serve here.
func (c GitLabClient) getJSON(ctx context.Context, token, endpoint string, out any) (int, error) {
	client := c.HTTPClient
	if client == nil {
		client = defaultHTTPClient()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, &RetryableError{Cause: err}
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("PRIVATE-TOKEN", token)
	response, err := client.Do(request)
	if err != nil {
		return 0, &RetryableError{Cause: err}
	}
	defer func() {
		_ = response.Body.Close()
	}()
	if gitlabRateLimited(response) {
		delay := gitlabRetryAfter(response.Header, time.Now())
		if delay <= 0 {
			delay = time.Minute
		}
		return 0, RateLimitError{RetryAfter: delay}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		httpErr := HTTPError{Provider: "gitlab_ci", StatusCode: response.StatusCode, RetryAfter: parseRetryAfterHeader(response.Header.Get("Retry-After"), time.Now())}
		if response.StatusCode >= 500 {
			return 0, &RetryableError{Cause: httpErr}
		}
		return 0, httpErr
	}
	total, _ := strconv.Atoi(strings.TrimSpace(response.Header.Get("X-Total")))
	decoder := json.NewDecoder(response.Body)
	decoder.UseNumber()
	if err := decoder.Decode(out); err != nil {
		return 0, &RetryableError{Cause: fmt.Errorf("decode gitlab_ci response: %w", err)}
	}
	return total, nil
}

func gitlabRateLimited(response *http.Response) bool {
	return response.StatusCode == http.StatusTooManyRequests
}

func gitlabRetryAfter(header http.Header, now time.Time) time.Duration {
	if retryAfter := parseRetryAfterHeader(header.Get("Retry-After"), now); retryAfter > 0 {
		return retryAfter
	}
	if reset := strings.TrimSpace(header.Get("RateLimit-ResetTime")); reset != "" {
		if parsed, err := http.ParseTime(reset); err == nil {
			if delay := parsed.UTC().Sub(now); delay > 0 {
				return delay
			}
		}
	}
	return 0
}

// normalizeGitLabPipeline maps one pipeline detail object to the Evidence
// seam. GitLab pipelines have no in-place retry-attempt concept (re-running
// creates a new pipeline ID), so run_attempt is always "1", mirroring the
// in-tree gitlabSharedPayload.
func normalizeGitLabPipeline(pipeline map[string]any) (Evidence, error) {
	pipelineID, err := numericProviderID(pipeline["id"])
	if err != nil || strings.TrimSpace(pipelineID) == "" {
		return Evidence{}, fmt.Errorf("gitlab ci pipeline.id: %v", pipeline["id"])
	}
	runNumber, err := numericProviderID(pipeline["iid"])
	if err != nil {
		return Evidence{}, fmt.Errorf("gitlab ci pipeline.iid: %v", pipeline["iid"])
	}
	var user map[string]any
	if raw, ok := pipeline["user"].(map[string]any); ok {
		user = raw
	}
	webURL := anyString(pipeline["web_url"])
	evidence := Evidence{
		Provider:   string(ProviderGitLabCI),
		RunID:      pipelineID,
		RunAttempt: "1",
		Repository: RepositoryEvidence{WebURL: webURL},
		RunNumber:  runNumber,
		Source:     anyString(pipeline["source"]),
		Status:     anyString(pipeline["status"]),
		Branch:     anyString(pipeline["ref"]),
		CommitSHA:  anyString(pipeline["sha"]),
		RunURL:     webURL,
		StartedAt:  anyString(pipeline["started_at"]),
		UpdatedAt:  anyString(pipeline["updated_at"]),
		Actor:      anyString(user["username"]),
	}
	evidence.Conclusion = evidence.Status
	evidence.Event = evidence.Source
	if gitlabProjectURL(webURL) == "" || evidence.CommitSHA == "" {
		evidence.Warnings = append(evidence.Warnings, WarningEvidence{
			Key:     "run:anchors",
			Reason:  "run_missing_repository_or_commit",
			Message: "run metadata omitted repository locator or commit SHA",
		})
	}
	return evidence, nil
}

// normalizeGitLabJobs appends one pipeline's bounded jobs to its evidence.
// GitLab reports job artifacts inline with no digest at this level, so
// every emitted artifact carries its explicit missing-digest warning,
// matching the real API shape rather than a collector gap. Artifacts
// without a filename degrade to a missing-id warning and are skipped.
func normalizeGitLabJobs(evidence *Evidence, jobs []map[string]any, partial bool) {
	evidence.JobsPartial = partial
	for index, job := range jobs {
		jobID, err := numericProviderID(job["id"])
		if err != nil || strings.TrimSpace(jobID) == "" {
			evidence.Warnings = append(evidence.Warnings, WarningEvidence{
				Key:     fmt.Sprintf("job:%d", index),
				Reason:  "job_missing_id",
				Message: "job metadata omitted provider job ID",
			})
			continue
		}
		entry := JobEvidence{
			ID:          jobID,
			Name:        anyString(job["name"]),
			Stage:       anyString(job["stage"]),
			Status:      anyString(job["status"]),
			StartedAt:   anyString(job["started_at"]),
			CompletedAt: anyString(job["finished_at"]),
		}
		if raw, ok := job["artifacts"].([]any); ok {
			for artifactIndex, rawArtifact := range raw {
				artifact, ok := rawArtifact.(map[string]any)
				if !ok {
					continue
				}
				filename := anyString(artifact["filename"])
				if filename == "" {
					evidence.Warnings = append(evidence.Warnings, WarningEvidence{
						Key:     fmt.Sprintf("artifact:%s:%d", jobID, artifactIndex),
						Reason:  "artifact_missing_id",
						Message: "artifact metadata omitted provider filename",
					})
					continue
				}
				artifactID := jobID + ":" + filename
				entry.InlineArtifacts = append(entry.InlineArtifacts, ArtifactEvidence{
					ID:        artifactID,
					Name:      filename,
					Type:      anyString(artifact["file_type"]),
					SizeBytes: anyInt64(artifact["size"]),
				})
			}
		}
		evidence.Jobs = append(evidence.Jobs, entry)
	}
}

// gitlabProjectURL strips GitLab's "/-/<resource>" suffix from a pipeline
// or job web_url, leaving the project root URL. It ports the in-tree
// gitlabProjectURLFromWebURL: GitLab always renders pipeline pages under
// the project root at "/-/pipelines/<id>".
func gitlabProjectURL(webURL string) string {
	trimmed := strings.TrimSpace(webURL)
	if trimmed == "" {
		return ""
	}
	index := strings.Index(trimmed, "/-/")
	if index <= 0 {
		return ""
	}
	return trimmed[:index]
}
