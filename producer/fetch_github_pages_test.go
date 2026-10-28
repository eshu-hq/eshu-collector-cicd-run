// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

// Deployment sub-pages, seam warnings, and fetch edge signals:
// the GitHub fetch paths that degrade explicitly rather than
// through the main window-outcome table.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The claim deadline bounds the fetch: an already-expired deadline fails
// the fetch without touching the provider.
func TestFetchGitHubHonorsClaimDeadline(t *testing.T) {
	t.Parallel()

	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"total_count":0,"workflow_runs":[]}`))
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Minute))
	defer cancel()
	_, err := (GitHubClient{HTTPClient: server.Client()}).FetchWindow(
		ctx, githubFetchTarget(server.URL), "fetch-token")
	if err == nil {
		t.Fatal("FetchWindow(expired deadline) = nil, want a fetch-bound failure")
	}
	if calls != 0 {
		t.Errorf("provider calls = %d, want 0: the expired deadline binds before any read", calls)
	}
}

// A throttle on the artifacts page commits the run with its jobs: the
// fetched prefix is partial, never dropped.
func TestFetchGitHubArtifactsRateLimitHalts(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/example/repo/actions/runs":
			_, _ = w.Write([]byte(`{"total_count":1,"workflow_runs":[` + githubRunPayload() + `]}`))
		case "/repos/example/repo/actions/runs/1001/jobs":
			_, _ = w.Write([]byte(githubJobsPayload()))
		case "/repos/example/repo/actions/runs/1001/artifacts":
			w.Header().Set("Retry-After", "30")
			http.Error(w, "throttled", http.StatusTooManyRequests)
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
		t.Error("RateLimited = false, want true from the artifacts throttle")
	}
	if len(window.Evidences) != 1 {
		t.Fatalf("len(Evidences) = %d, want 1", len(window.Evidences))
	}
	evidence := window.Evidences[0]
	if len(evidence.Jobs) != 1 {
		t.Errorf("Jobs = %d, want the fetched jobs kept", len(evidence.Jobs))
	}
	if !evidence.ArtifactsPartial {
		t.Error("ArtifactsPartial = false, want true for the throttled page")
	}
}

// A run without provider identity is a malformed payload: retryable,
// never an emission with a blank run id.
func TestFetchGitHubBadRunIDIsRetryable(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/example/repo/actions/runs" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"total_count":1,"workflow_runs":[{"name":"nameless"}]}`))
	}))
	t.Cleanup(server.Close)

	_, err := (GitHubClient{HTTPClient: server.Client()}).FetchWindow(
		context.Background(), githubFetchTarget(server.URL), "fetch-token")
	var retryable *RetryableError
	if !errors.As(err, &retryable) {
		t.Fatalf("FetchWindow() error = %T, want *RetryableError", err)
	}
}

// Deployment sub-page signals: a refused statuses read degrades to the
// permission warning, a full statuses page warns truncation, a full
// deployments page warns the window cut, and a deployment missing
// required fields warns instead of emitting.
func TestFetchGitHubDeploymentSubPageSignals(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/example/repo/actions/runs":
			_, _ = w.Write([]byte(`{"total_count":1,"workflow_runs":[` + githubRunPayload() + `]}`))
		case "/repos/example/repo/actions/runs/1001/jobs":
			_, _ = w.Write([]byte(`{"total_count":0,"jobs":[]}`))
		case "/repos/example/repo/actions/runs/1001/artifacts":
			_, _ = w.Write([]byte(`{"total_count":0,"artifacts":[]}`))
		case "/repos/example/repo/deployments":
			_, _ = w.Write([]byte(`[` +
				`{"id":4001,"sha":"` + fetchTestSHA + `","environment":"production"},` +
				`{"id":4002},` +
				`{"id":4003,"sha":"` + fetchTestSHA + `","environment":"staging"}` +
				`]`))
		case "/repos/example/repo/deployments/4001/statuses":
			http.Error(w, "deployments permission missing", http.StatusForbidden)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	// Refused statuses degrade the whole deployments phase to the
	// permission warning with run facts kept.
	window, err := (GitHubClient{HTTPClient: server.Client()}).FetchWindow(
		context.Background(), githubFetchTarget(server.URL), "fetch-token")
	if err != nil {
		t.Fatalf("FetchWindow() error = %v, want nil", err)
	}
	if len(window.Evidences[0].Deployments) != 0 {
		t.Errorf("Deployments = %+v, want none after the statuses refusal", window.Evidences[0].Deployments)
	}
	found := false
	for _, warning := range window.Evidences[0].Warnings {
		if warning.Reason == "deployment_permission_denied" {
			found = true
		}
	}
	if !found {
		t.Errorf("Warnings = %+v, want deployment_permission_denied", window.Evidences[0].Warnings)
	}
}

func TestFetchGitHubDeploymentWindowWarnings(t *testing.T) {
	t.Parallel()

	statuses := `[`
	for i := 1; i <= maxDeploymentStatuses; i++ {
		if i > 1 {
			statuses += `,`
		}
		statuses += `{"id":` + strconv.Itoa(i) + `,"state":"success"}`
	}
	statuses += `]`
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/example/repo/actions/runs":
			_, _ = w.Write([]byte(`{"total_count":1,"workflow_runs":[` + githubRunPayload() + `]}`))
		case "/repos/example/repo/actions/runs/1001/jobs":
			_, _ = w.Write([]byte(`{"total_count":0,"jobs":[]}`))
		case "/repos/example/repo/actions/runs/1001/artifacts":
			_, _ = w.Write([]byte(`{"total_count":0,"artifacts":[]}`))
		case "/repos/example/repo/deployments":
			// One deployment against a MaxDeployments of 1: the
			// full-page heuristic reports the window cut.
			_, _ = w.Write([]byte(`[{"id":4001,"sha":"` + fetchTestSHA + `","environment":"production"}]`))
		case "/repos/example/repo/deployments/4001/statuses":
			_, _ = w.Write([]byte(statuses))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	target := githubFetchTarget(server.URL)
	target.MaxDeployments = 1
	window, err := (GitHubClient{HTTPClient: server.Client()}).FetchWindow(context.Background(), target, "fetch-token")
	if err != nil {
		t.Fatalf("FetchWindow() error = %v, want nil", err)
	}
	reasons := map[string]bool{}
	for _, warning := range window.Evidences[0].Warnings {
		reasons[warning.Reason] = true
	}
	if !reasons["deployment_statuses_truncated"] {
		t.Errorf("Warnings = %+v, want deployment_statuses_truncated for the full statuses page", window.Evidences[0].Warnings)
	}
	if !reasons["deployments_truncated"] {
		t.Errorf("Warnings = %+v, want deployments_truncated for the full deployments page", window.Evidences[0].Warnings)
	}
}

// The X-RateLimit-Reset epoch hint feeds the retry guidance when no
// Retry-After header is present.
func TestFetchGitHubResetEpochRetryAfter(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "4102444800")
		http.Error(w, "throttled", http.StatusForbidden)
	}))
	t.Cleanup(server.Close)

	_, err := (GitHubClient{HTTPClient: server.Client()}).FetchWindow(
		context.Background(), githubFetchTarget(server.URL), "fetch-token")
	var retryable *RetryableError
	if !errors.As(err, &retryable) {
		t.Fatalf("FetchWindow() error = %T, want *RetryableError", err)
	}
	if retryable.RetryAfter <= 0 {
		t.Errorf("RetryAfter = %v, want the reset-epoch guidance", retryable.RetryAfter)
	}
}

// Artifact seam warnings: identity-less and run-mismatched artifacts
// degrade explicitly while well-formed ones emit.
func TestNormalizeGitHubArtifactWarnings(t *testing.T) {
	t.Parallel()

	var run map[string]any
	decoder := json.NewDecoder(strings.NewReader(githubRunPayload()))
	decoder.UseNumber()
	if err := decoder.Decode(&run); err != nil {
		t.Fatalf("decode run payload: %v", err)
	}
	evidence, err := normalizeGitHubRun(run)
	if err != nil {
		t.Fatalf("normalizeGitHubRun() error = %v, want nil", err)
	}
	normalizeGitHubArtifacts(&evidence, run, []map[string]any{
		{"name": "no-id"},
		{"id": 3002, "name": "wrong-run", "workflow_run": map[string]any{"id": 9999, "head_sha": fetchTestSHA}},
		{
			"id": 3003, "name": "no-digest", "size_in_bytes": 64,
			"archive_download_url": "https://example.com/dl/3003?sig=signed-canary",
			"workflow_run":         map[string]any{"id": 1001, "head_sha": fetchTestSHA},
		},
	}, false)
	if len(evidence.Artifacts) != 1 || evidence.Artifacts[0].ID != "3003" {
		t.Fatalf("Artifacts = %+v, want only the well-formed artifact", evidence.Artifacts)
	}
	if got := evidence.Artifacts[0].DownloadURL; got != "https://example.com/dl/3003" {
		t.Errorf("DownloadURL = %q, want the query-stripped URL", got)
	}
	reasons := map[string]bool{}
	for _, warning := range evidence.Warnings {
		reasons[warning.Reason] = true
	}
	for _, want := range []string{"artifact_missing_id", "artifact_run_mismatch", "artifact_missing_digest"} {
		if !reasons[want] {
			t.Errorf("Warnings = %+v, want %q", evidence.Warnings, want)
		}
	}
}

func TestProviderScalarVariants(t *testing.T) {
	t.Parallel()

	if got := anyInt64(json.Number("128")); got != 128 {
		t.Errorf("anyInt64(json.Number) = %d, want 128", got)
	}
	if got := anyInt64(12.0); got != 12 {
		t.Errorf("anyInt64(float64) = %d, want 12", got)
	}
	if got := anyInt64("not-a-number"); got != 0 {
		t.Errorf("anyInt64(string) = %d, want 0", got)
	}
	if got, err := numericProviderID(12.5); err == nil || got != "" {
		t.Errorf("numericProviderID(12.5) = %q, %v; want rejection", got, err)
	}
	if got := stripURLQuery("https://example.com/dl/1?sig=x#frag"); got != "https://example.com/dl/1" {
		t.Errorf("stripURLQuery() = %q, want the bare URL", got)
	}
	if got := stripURLQuery(":::not a url::"); got != "" {
		t.Errorf("stripURLQuery(bad) = %q, want empty", got)
	}
	deployment := map[string]any{"id": 1, "sha": "abc", "environment": "production"}
	if _, err := normalizeGitHubDeployment(deployment, []map[string]any{{"id": []any{}}}); err == nil {
		t.Error("normalizeGitHubDeployment(bad status id) = nil, want rejection")
	}
	if _, err := normalizeGitHubDeployment(map[string]any{"id": []any{}}, nil); err == nil {
		t.Error("normalizeGitHubDeployment(bad deployment id) = nil, want rejection")
	}
}
