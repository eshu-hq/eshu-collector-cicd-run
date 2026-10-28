// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Canned provider payloads reuse the in-tree client-test shapes
// (ghactionsruntime/client_test.go: total_count wrappers, per-run
// jobs/artifacts sub-resources) with full run/job/artifact/deployment
// fields so normalization, caps, and the SHIM_MAP outcome signals prove
// against the same wire the in-tree collector reads.

const (
	fetchTestSHA   = "0123456789abcdef0123456789abcdef01234567"
	fetchTestScope = "ci-cd:github-actions:example-org/example-repo"
)

func githubRunPayload() string {
	return `{"id":1001,"workflow_id":42,"name":"Publish","run_attempt":1,` +
		`"run_number":7,"event":"push","status":"completed","conclusion":"success",` +
		`"head_branch":"main","head_sha":"` + fetchTestSHA + `",` +
		`"run_started_at":"2026-10-27T00:00:00Z","updated_at":"2026-10-27T00:05:00Z",` +
		`"html_url":"https://github.com/example/repo/actions/runs/1001",` +
		`"path":".github/workflows/ci.yml",` +
		`"repository":{"full_name":"example/repo","html_url":"https://github.com/example/repo"},` +
		`"actor":{"login":"octocat"}}`
}

func githubJobsPayload() string {
	return `{"total_count":1,"jobs":[{` +
		`"id":2001,"name":"build","status":"completed","conclusion":"success",` +
		`"started_at":"2026-10-27T00:01:00Z","completed_at":"2026-10-27T00:04:00Z",` +
		`"labels":["ubuntu-latest"],` +
		`"steps":[{"name":"Set up job","number":1,"status":"completed",` +
		`"conclusion":"success","started_at":"2026-10-27T00:01:00Z",` +
		`"completed_at":"2026-10-27T00:01:10Z"}]}]}`
}

func githubArtifactsPayload() string {
	return `{"total_count":1,"artifacts":[{` +
		`"id":3001,"name":"image-digest","size_in_bytes":128,"digest":"sha256:abc",` +
		`"artifact_type":"container_image",` +
		`"archive_download_url":"https://example.com/dl/3001?sig=signed-canary",` +
		`"expired":false,"created_at":"2026-10-27T00:05:00Z","expires_at":"2026-11-27T00:05:00Z",` +
		`"workflow_run":{"id":1001,"head_sha":"` + fetchTestSHA + `"}}]}`
}

func githubDeploymentsPayload() string {
	return `[{` +
		`"id":4001,"sha":"` + fetchTestSHA + `","ref":"main","task":"deploy",` +
		`"environment":"production","original_environment":"",` +
		`"description":"ship it","created_at":"2026-10-27T00:06:00Z",` +
		`"updated_at":"2026-10-27T00:07:00Z",` +
		`"transient_environment":false,"production_environment":true}]`
}

func githubDeploymentStatusesPayload() string {
	return `[{` +
		`"id":5001,"state":"success","environment":"production",` +
		`"description":"done","created_at":"2026-10-27T00:06:30Z",` +
		`"updated_at":"2026-10-27T00:07:00Z",` +
		`"environment_url":"https://example.com/app?token=signed-canary",` +
		`"log_url":"https://example.com/log"}]`
}

// completeGitHubServer serves a full window: one run plus its jobs,
// artifacts, deployments, and statuses. It fails the test on a missing or
// wrong Authorization header so every fetch test also proves the Bearer [REDACTED] travels.
func completeGitHubServer(t *testing.T, token string) *httptest.Server {
	t.Helper()

	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("Authorization = %q, want the configured Bearer", got)
		}
		switch r.URL.Path {
		case "/repos/example/repo/actions/runs":
			_, _ = w.Write([]byte(`{"total_count":1,"workflow_runs":[` + githubRunPayload() + `]}`))
		case "/repos/example/repo/actions/runs/1001/jobs":
			_, _ = w.Write([]byte(githubJobsPayload()))
		case "/repos/example/repo/actions/runs/1001/artifacts":
			_, _ = w.Write([]byte(githubArtifactsPayload()))
		case "/repos/example/repo/deployments":
			_, _ = w.Write([]byte(githubDeploymentsPayload()))
		case "/repos/example/repo/deployments/4001/statuses":
			_, _ = w.Write([]byte(githubDeploymentStatusesPayload()))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}

func githubFetchTarget(apiBaseURL string) ResolvedTarget {
	return ResolvedTarget{
		Provider:       ProviderGitHubActions,
		ScopeID:        fetchTestScope,
		Repository:     "example/repo",
		APIBaseURL:     apiBaseURL,
		SourceURI:      "https://github.com/example/repo",
		MaxRuns:        1,
		MaxJobs:        25,
		MaxArtifacts:   25,
		MaxDeployments: 10,
	}
}

func TestFetchGitHubWindowComplete(t *testing.T) {
	t.Parallel()

	server := completeGitHubServer(t, "fetch-token")
	t.Cleanup(server.Close)

	window, err := (GitHubClient{HTTPClient: server.Client()}).FetchWindow(
		context.Background(), githubFetchTarget(server.URL), "fetch-token")
	if err != nil {
		t.Fatalf("FetchWindow() error = %v, want nil", err)
	}
	if window.Truncated || window.RateLimited {
		t.Fatalf("Window flags = truncated %v rate_limited %v, want both false", window.Truncated, window.RateLimited)
	}
	if len(window.Evidences) != 1 {
		t.Fatalf("len(Evidences) = %d, want 1", len(window.Evidences))
	}
	evidence := window.Evidences[0]
	if evidence.Provider != string(ProviderGitHubActions) {
		t.Errorf("Provider = %q, want github_actions", evidence.Provider)
	}
	if evidence.RunID != "1001" || evidence.Branch != "main" || evidence.Actor != "octocat" {
		t.Errorf("run = %q/%q/%q, want 1001/main/octocat", evidence.RunID, evidence.Branch, evidence.Actor)
	}
	if evidence.CommitSHA != fetchTestSHA || evidence.RunNumber != "7" || evidence.RunAttempt != "1" {
		t.Errorf("run identity = %q/%q/%q, want sha/7/1", evidence.CommitSHA, evidence.RunNumber, evidence.RunAttempt)
	}
	if evidence.Repository.FullName != "example/repo" {
		t.Errorf("Repository.FullName = %q, want example/repo", evidence.Repository.FullName)
	}
	if evidence.Workflow == nil || evidence.Workflow.ID != "42" || evidence.Workflow.Path != ".github/workflows/ci.yml" {
		t.Errorf("Workflow = %+v, want id 42 with workflow path", evidence.Workflow)
	}
	if len(evidence.Jobs) != 1 || evidence.Jobs[0].ID != "2001" {
		t.Fatalf("Jobs = %+v, want the single build job", evidence.Jobs)
	}
	job := evidence.Jobs[0]
	if len(job.Steps) != 1 || job.Steps[0].Number != "1" {
		t.Errorf("Steps = %+v, want the single numbered step", job.Steps)
	}
	if len(evidence.Artifacts) != 1 || evidence.Artifacts[0].ID != "3001" {
		t.Fatalf("Artifacts = %+v, want the single artifact", evidence.Artifacts)
	}
	if got := evidence.Artifacts[0].DownloadURL; got != "https://example.com/dl/3001" {
		t.Errorf("DownloadURL = %q, want the query-stripped URL (in-tree sanitizeArtifacts)", got)
	}
	if len(evidence.Deployments) != 1 {
		t.Fatalf("Deployments = %+v, want the single status row", evidence.Deployments)
	}
	deployment := evidence.Deployments[0]
	if deployment.ID != "4001" || deployment.StatusID != "5001" || deployment.Environment != "production" {
		t.Errorf("deployment = %+v, want 4001/5001/production", deployment)
	}
	if got := deployment.EnvironmentURL; got != "https://example.com/app" {
		t.Errorf("EnvironmentURL = %q, want the query-stripped URL", got)
	}
	if evidence.JobsPartial || evidence.ArtifactsPartial {
		t.Errorf("partial flags = jobs %v artifacts %v, want both false", evidence.JobsPartial, evidence.ArtifactsPartial)
	}
}

func TestFetchGitHubWindowTruncated(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/example/repo/actions/runs":
			_, _ = w.Write([]byte(`{"total_count":5,"workflow_runs":[` + githubRunPayload() + `]}`))
		case "/repos/example/repo/actions/runs/1001/jobs":
			_, _ = w.Write([]byte(githubJobsPayload()))
		case "/repos/example/repo/actions/runs/1001/artifacts":
			_, _ = w.Write([]byte(`{"total_count":0,"artifacts":[]}`))
		case "/repos/example/repo/deployments":
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	window, err := (GitHubClient{HTTPClient: server.Client()}).FetchWindow(
		context.Background(), githubFetchTarget(server.URL), "fetch-token")
	if err != nil {
		t.Fatalf("FetchWindow() error = %v, want nil", err)
	}
	if !window.Truncated {
		t.Error("Truncated = false, want true from total_count 5 over 1 fetched run")
	}
}

func TestFetchGitHubJobsPartial(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/example/repo/actions/runs":
			_, _ = w.Write([]byte(`{"total_count":1,"workflow_runs":[` + githubRunPayload() + `]}`))
		case "/repos/example/repo/actions/runs/1001/jobs":
			// total_count 2 over 1 returned job: the in-tree partial-page signal.
			_, _ = w.Write([]byte(`{"total_count":2,` + githubJobsPayload()[17:]))
		case "/repos/example/repo/actions/runs/1001/artifacts":
			_, _ = w.Write([]byte(`{"total_count":0,"artifacts":[]}`))
		case "/repos/example/repo/deployments":
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	window, err := (GitHubClient{HTTPClient: server.Client()}).FetchWindow(
		context.Background(), githubFetchTarget(server.URL), "fetch-token")
	if err != nil {
		t.Fatalf("FetchWindow() error = %v, want nil", err)
	}
	if len(window.Evidences) != 1 {
		t.Fatalf("len(Evidences) = %d, want 1", len(window.Evidences))
	}
	if !window.Evidences[0].JobsPartial {
		t.Error("JobsPartial = false, want true from total_count over returned jobs")
	}
}

func TestFetchGitHubRateLimitMidWindowCommitsPartial(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/example/repo/actions/runs":
			_, _ = w.Write([]byte(`{"total_count":1,"workflow_runs":[` + githubRunPayload() + `]}`))
		case "/repos/example/repo/actions/runs/1001/jobs":
			w.Header().Set("Retry-After", "60")
			http.Error(w, "throttled", http.StatusForbidden)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	window, err := (GitHubClient{HTTPClient: server.Client()}).FetchWindow(
		context.Background(), githubFetchTarget(server.URL), "fetch-token")
	if err != nil {
		t.Fatalf("FetchWindow() error = %v, want nil with partial commit", err)
	}
	if !window.RateLimited {
		t.Error("RateLimited = false, want true from the mid-window 403 throttle")
	}
	if len(window.Evidences) != 1 {
		t.Fatalf("len(Evidences) = %d, want the fetched run committed partial", len(window.Evidences))
	}
	if !window.Evidences[0].JobsPartial {
		t.Error("JobsPartial = false, want true for the throttled jobs page")
	}
}

func TestFetchGitHubFirstRequestRateLimitedIsRetryable(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "throttled", http.StatusTooManyRequests)
	}))
	t.Cleanup(server.Close)

	_, err := (GitHubClient{HTTPClient: server.Client()}).FetchWindow(
		context.Background(), githubFetchTarget(server.URL), "fetch-token")
	if err == nil {
		t.Fatal("FetchWindow() = nil, want retryable with no page committed")
	}
	var retryable *RetryableError
	if !errors.As(err, &retryable) {
		t.Fatalf("FetchWindow() error = %T, want *RetryableError", err)
	}
}

func TestFetchGitHubTransientIsRetryable(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream fault", http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)

	_, err := (GitHubClient{HTTPClient: server.Client()}).FetchWindow(
		context.Background(), githubFetchTarget(server.URL), "fetch-token")
	if err == nil {
		t.Fatal("FetchWindow() = nil, want retryable on 502")
	}
	var retryable *RetryableError
	if !errors.As(err, &retryable) {
		t.Fatalf("FetchWindow() error = %T, want *RetryableError", err)
	}
}

func TestFetchGitHubAuthRefusedIsTerminal(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		status int
		reason string
	}{
		{http.StatusUnauthorized, ReasonProviderDenied},
		{http.StatusForbidden, ReasonProviderDenied},
		{http.StatusNotFound, ReasonProviderNotFound},
		{http.StatusUnprocessableEntity, ReasonProviderRejected},
	} {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			t.Parallel()

			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "provider body mentions fetch-token and example/repo", test.status)
			}))
			t.Cleanup(server.Close)

			_, err := (GitHubClient{HTTPClient: server.Client()}).FetchWindow(
				context.Background(), githubFetchTarget(server.URL), "fetch-token")
			if err == nil {
				t.Fatalf("FetchWindow() = nil, want terminal %s", test.reason)
			}
			var terminal *TerminalError
			if !errors.As(err, &terminal) {
				t.Fatalf("FetchWindow() error = %T, want *TerminalError", err)
			}
			if terminal.Reason != test.reason {
				t.Errorf("reason = %q, want %q", terminal.Reason, test.reason)
			}
			if terminal.FailureClass() != "invalid_config."+test.reason {
				t.Errorf("FailureClass() = %q, want the invalid_config sub-class", terminal.FailureClass())
			}
			if strings.Contains(err.Error(), "fetch-token") || strings.Contains(err.Error(), "example/repo") {
				t.Errorf("FetchWindow() error leaked provider body or token: %q", err.Error())
			}
		})
	}
}

func TestFetchGitHubDeploymentsForbiddenKeepsRunFacts(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/example/repo/actions/runs":
			_, _ = w.Write([]byte(`{"total_count":1,"workflow_runs":[` + githubRunPayload() + `]}`))
		case "/repos/example/repo/actions/runs/1001/jobs":
			_, _ = w.Write([]byte(githubJobsPayload()))
		case "/repos/example/repo/actions/runs/1001/artifacts":
			_, _ = w.Write([]byte(`{"total_count":0,"artifacts":[]}`))
		case "/repos/example/repo/deployments":
			// Deployments is a separate GitHub permission from Actions
			// read: a 403 here must not drop the collected run facts.
			http.Error(w, "deployments permission missing", http.StatusForbidden)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	window, err := (GitHubClient{HTTPClient: server.Client()}).FetchWindow(
		context.Background(), githubFetchTarget(server.URL), "fetch-token")
	if err != nil {
		t.Fatalf("FetchWindow() error = %v, want nil with run facts kept", err)
	}
	if len(window.Evidences) != 1 {
		t.Fatalf("len(Evidences) = %d, want 1", len(window.Evidences))
	}
	if len(window.Evidences[0].Deployments) != 0 {
		t.Errorf("Deployments = %+v, want none after the permission refusal", window.Evidences[0].Deployments)
	}
	found := false
	for _, warning := range window.Evidences[0].Warnings {
		if warning.Reason == "deployment_permission_denied" {
			found = true
		}
	}
	if !found {
		t.Errorf("Warnings = %+v, want the deployment_permission_denied warning", window.Evidences[0].Warnings)
	}
}

func TestFetchGitHubEmptyWindowIsNotAnError(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/example/repo/actions/runs" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"total_count":0,"workflow_runs":[]}`))
	}))
	t.Cleanup(server.Close)

	window, err := (GitHubClient{HTTPClient: server.Client()}).FetchWindow(
		context.Background(), githubFetchTarget(server.URL), "fetch-token")
	if err != nil {
		t.Fatalf("FetchWindow() error = %v, want nil for an empty window (unchanged generation)", err)
	}
	if len(window.Evidences) != 0 {
		t.Errorf("len(Evidences) = %d, want 0", len(window.Evidences))
	}
}
