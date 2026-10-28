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
	"time"
)

// Canned GitLab payloads reuse the in-tree client-test shapes
// (gitlabciruntime/client_test.go: pipeline list plus detail plus jobs,
// X-Total pagination headers, PRIVATE-TOKEN auth) so the satellite fetch
// proves against the same wire the in-tree collector reads.

const gitlabTestSHA = "1f2e3d4c5b6a79889706a5b4c3d2e1f00f1e2d3c"

func gitlabPipelineListPayload() string {
	return `[{"id":7700,"iid":42,"project_id":1,"status":"success",` +
		`"source":"push","ref":"main","sha":"` + gitlabTestSHA + `",` +
		`"web_url":"https://gitlab.example.com/eshu-hq/demo/-/pipelines/7700",` +
		`"created_at":"2026-06-07T14:58:00Z","updated_at":"2026-06-07T15:00:00Z"}]`
}

func gitlabPipelineDetailPayload() string {
	return `{"id":7700,"iid":42,"project_id":1,"status":"success",` +
		`"source":"push","ref":"main","sha":"` + gitlabTestSHA + `",` +
		`"web_url":"https://gitlab.example.com/eshu-hq/demo/-/pipelines/7700",` +
		`"created_at":"2026-06-07T14:58:00Z","updated_at":"2026-06-07T15:00:00Z",` +
		`"started_at":"2026-06-07T14:59:00Z","finished_at":"2026-06-07T15:00:00Z",` +
		`"user":{"username":"builder"}}`
}

func gitlabJobsPayload() string {
	return `[{"id":9001,"name":"build","stage":"build","status":"success",` +
		`"created_at":"2026-06-07T14:58:10Z","started_at":"2026-06-07T14:58:20Z",` +
		`"finished_at":"2026-06-07T14:59:50Z",` +
		`"web_url":"https://gitlab.example.com/eshu-hq/demo/-/jobs/9001",` +
		`"artifacts":[{"file_type":"archive","size":128,` +
		`"filename":"artifacts.zip","file_format":"zip"}]}]`
}

// completeGitLabServer serves a full window: the pipeline list, the one
// pipeline's detail, and its jobs. It fails the test on a missing or
// wrong PRIVATE-TOKEN header so every fetch test also proves the token
// travels on the GitLab header, never in query or body.
func completeGitLabServer(t *testing.T, token string) *httptest.Server {
	t.Helper()

	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("PRIVATE-TOKEN"); got != token {
			t.Errorf("PRIVATE-TOKEN = %q, want the configured token", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want empty: GitLab auth travels on PRIVATE-TOKEN", got)
		}
		switch r.URL.EscapedPath() {
		case "/projects/eshu-hq%2Fdemo/pipelines":
			w.Header().Set("X-Total", "1")
			_, _ = w.Write([]byte(gitlabPipelineListPayload()))
		case "/projects/eshu-hq%2Fdemo/pipelines/7700":
			_, _ = w.Write([]byte(gitlabPipelineDetailPayload()))
		case "/projects/eshu-hq%2Fdemo/pipelines/7700/jobs":
			w.Header().Set("X-Total", "1")
			_, _ = w.Write([]byte(gitlabJobsPayload()))
		default:
			t.Errorf("unexpected path %s", r.URL.EscapedPath())
			http.NotFound(w, r)
		}
	}))
}

func gitlabFetchTarget(apiBaseURL string) ResolvedTarget {
	return ResolvedTarget{
		Provider:   ProviderGitLabCI,
		ScopeID:    "gitlab-ci://gitlab.com/eshu-hq/demo",
		Repository: "eshu-hq/demo",
		APIBaseURL: apiBaseURL,
		SourceURI:  "https://gitlab.com/eshu-hq/demo",
		MaxRuns:    1,
		MaxJobs:    25,
	}
}

func TestFetchGitLabWindowComplete(t *testing.T) {
	t.Parallel()

	server := completeGitLabServer(t, "fetch-token")
	t.Cleanup(server.Close)

	window, err := (GitLabClient{HTTPClient: server.Client()}).FetchWindow(
		context.Background(), gitlabFetchTarget(server.URL), "fetch-token")
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
	if evidence.Provider != string(ProviderGitLabCI) {
		t.Errorf("Provider = %q, want gitlab_ci", evidence.Provider)
	}
	if evidence.RunID != "7700" || evidence.RunNumber != "42" || evidence.RunAttempt != "1" {
		t.Errorf("pipeline identity = %q/%q/%q, want 7700/42/1", evidence.RunID, evidence.RunNumber, evidence.RunAttempt)
	}
	if evidence.Branch != "main" || evidence.CommitSHA != gitlabTestSHA || evidence.Actor != "builder" {
		t.Errorf("pipeline = %q/%q/%q, want main/sha/builder", evidence.Branch, evidence.CommitSHA, evidence.Actor)
	}
	if evidence.Status != "success" || evidence.Conclusion != "success" || evidence.Source != "push" {
		t.Errorf("status = %q/%q/%q, want success/success/push", evidence.Status, evidence.Conclusion, evidence.Source)
	}
	if evidence.Repository.WebURL != "https://gitlab.example.com/eshu-hq/demo/-/pipelines/7700" {
		t.Errorf("Repository.WebURL = %q, want the pipeline web_url", evidence.Repository.WebURL)
	}
	if len(evidence.Jobs) != 1 || evidence.Jobs[0].ID != "9001" || evidence.Jobs[0].Stage != "build" {
		t.Fatalf("Jobs = %+v, want the single build-stage job", evidence.Jobs)
	}
	if len(evidence.Jobs[0].InlineArtifacts) != 1 {
		t.Fatalf("InlineArtifacts = %+v, want the single inline artifact", evidence.Jobs[0].InlineArtifacts)
	}
	artifact := evidence.Jobs[0].InlineArtifacts[0]
	if artifact.ID != "9001:artifacts.zip" || artifact.Name != "artifacts.zip" || artifact.Type != "archive" {
		t.Errorf("artifact = %+v, want 9001:artifacts.zip/artifacts.zip/archive", artifact)
	}
	if evidence.JobsPartial {
		t.Error("JobsPartial = true, want false (X-Total matched fetched jobs)")
	}
}

func TestFetchGitLabWindowTruncated(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.EscapedPath() {
		case "/projects/eshu-hq%2Fdemo/pipelines":
			w.Header().Set("X-Total", "9")
			_, _ = w.Write([]byte(gitlabPipelineListPayload()))
		case "/projects/eshu-hq%2Fdemo/pipelines/7700":
			_, _ = w.Write([]byte(gitlabPipelineDetailPayload()))
		case "/projects/eshu-hq%2Fdemo/pipelines/7700/jobs":
			w.Header().Set("X-Total", "1")
			_, _ = w.Write([]byte(gitlabJobsPayload()))
		default:
			t.Errorf("unexpected path %s", r.URL.EscapedPath())
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	window, err := (GitLabClient{HTTPClient: server.Client()}).FetchWindow(
		context.Background(), gitlabFetchTarget(server.URL), "fetch-token")
	if err != nil {
		t.Fatalf("FetchWindow() error = %v, want nil", err)
	}
	if !window.Truncated {
		t.Error("Truncated = false, want true from X-Total 9 over 1 fetched pipeline")
	}
}

func TestFetchGitLabRateLimitMidWindowCommitsPartial(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.EscapedPath() {
		case "/projects/eshu-hq%2Fdemo/pipelines":
			w.Header().Set("X-Total", "1")
			_, _ = w.Write([]byte(gitlabPipelineListPayload()))
		case "/projects/eshu-hq%2Fdemo/pipelines/7700":
			_, _ = w.Write([]byte(gitlabPipelineDetailPayload()))
		case "/projects/eshu-hq%2Fdemo/pipelines/7700/jobs":
			w.Header().Set("Retry-After", "30")
			http.Error(w, "throttled", http.StatusTooManyRequests)
		default:
			t.Errorf("unexpected path %s", r.URL.EscapedPath())
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	window, err := (GitLabClient{HTTPClient: server.Client()}).FetchWindow(
		context.Background(), gitlabFetchTarget(server.URL), "fetch-token")
	if err != nil {
		t.Fatalf("FetchWindow() error = %v, want nil with partial commit", err)
	}
	if !window.RateLimited {
		t.Error("RateLimited = false, want true from the mid-window 429")
	}
	if len(window.Evidences) != 1 || !window.Evidences[0].JobsPartial {
		t.Errorf("Evidences = %+v, want the pipeline committed with partial jobs", window.Evidences)
	}
}

func TestFetchGitLabFailuresClassify(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		status int
		// wantTerminal is empty for retryable outcomes.
		wantTerminal string
	}{
		{"first-request throttle is retryable", http.StatusTooManyRequests, ""},
		{"transient is retryable", http.StatusBadGateway, ""},
		{"refused credential is terminal", http.StatusUnauthorized, ReasonProviderDenied},
		{"forbidden project is terminal", http.StatusForbidden, ReasonProviderDenied},
		{"missing pipeline is terminal", http.StatusNotFound, ReasonProviderNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "provider body mentions fetch-token and eshu-hq/demo", test.status)
			}))
			t.Cleanup(server.Close)

			_, err := (GitLabClient{HTTPClient: server.Client()}).FetchWindow(
				context.Background(), gitlabFetchTarget(server.URL), "fetch-token")
			if err == nil {
				t.Fatalf("FetchWindow() = nil, want a classified failure for %d", test.status)
			}
			if test.wantTerminal == "" {
				var retryable *RetryableError
				if !errors.As(err, &retryable) {
					t.Fatalf("FetchWindow() error = %T, want *RetryableError", err)
				}
				return
			}
			var terminal *TerminalError
			if !errors.As(err, &terminal) {
				t.Fatalf("FetchWindow() error = %T, want *TerminalError", err)
			}
			if terminal.Reason != test.wantTerminal {
				t.Errorf("reason = %q, want %q", terminal.Reason, test.wantTerminal)
			}
			if strings.Contains(err.Error(), "fetch-token") || strings.Contains(err.Error(), "eshu-hq/demo") {
				t.Errorf("FetchWindow() error leaked provider body or token: %q", err.Error())
			}
		})
	}
}

func TestFetchGitLabEmptyWindowIsNotAnError(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/projects/eshu-hq%2Fdemo/pipelines" {
			t.Errorf("unexpected path %s", r.URL.EscapedPath())
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(server.Close)

	window, err := (GitLabClient{HTTPClient: server.Client()}).FetchWindow(
		context.Background(), gitlabFetchTarget(server.URL), "fetch-token")
	if err != nil {
		t.Fatalf("FetchWindow() error = %v, want nil for an empty window (unchanged generation)", err)
	}
	if len(window.Evidences) != 0 {
		t.Errorf("len(Evidences) = %d, want 0", len(window.Evidences))
	}
}

// GitLab's documented RateLimit-ResetTime (RFC1123) feeds the retry
// guidance when no Retry-After header is present.
func TestFetchGitLabResetTimeRetryAfter(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("RateLimit-ResetTime", time.Now().UTC().Add(2*time.Minute).Format(http.TimeFormat))
		http.Error(w, "throttled", http.StatusTooManyRequests)
	}))
	t.Cleanup(server.Close)

	_, err := (GitLabClient{HTTPClient: server.Client()}).FetchWindow(
		context.Background(), gitlabFetchTarget(server.URL), "fetch-token")
	var retryable *RetryableError
	if !errors.As(err, &retryable) {
		t.Fatalf("FetchWindow() error = %T, want *RetryableError", err)
	}
	if retryable.RetryAfter <= 0 {
		t.Errorf("RetryAfter = %v, want the reset-time guidance", retryable.RetryAfter)
	}
}

// A pipeline without provider identity is a malformed payload, and jobs
// without identity degrade to explicit warnings while well-formed ones
// emit.
func TestFetchGitLabSeamWarnings(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.EscapedPath() {
		case "/projects/eshu-hq%2Fdemo/pipelines":
			w.Header().Set("X-Total", "1")
			_, _ = w.Write([]byte(`[{"id":7700}]`))
		case "/projects/eshu-hq%2Fdemo/pipelines/7700":
			_, _ = w.Write([]byte(gitlabPipelineDetailPayload()))
		case "/projects/eshu-hq%2Fdemo/pipelines/7700/jobs":
			w.Header().Set("X-Total", "4")
			_, _ = w.Write([]byte(`[` +
				`{"name":"no-id"},` +
				`{"id":9001,"name":"build","stage":"build","status":"success",` +
				`"started_at":"2026-06-07T14:58:20Z","finished_at":"2026-06-07T14:59:50Z",` +
				`"artifacts":[{"file_type":"archive","size":128},{"filename":"ok.zip","size":64}]},` +
				`{"id":9002,"name":"test","stage":"test","status":"success"}` +
				`]`))
		default:
			t.Errorf("unexpected path %s", r.URL.EscapedPath())
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	window, err := (GitLabClient{HTTPClient: server.Client()}).FetchWindow(
		context.Background(), gitlabFetchTarget(server.URL), "fetch-token")
	if err != nil {
		t.Fatalf("FetchWindow() error = %v, want nil", err)
	}
	if len(window.Evidences) != 1 {
		t.Fatalf("len(Evidences) = %d, want 1", len(window.Evidences))
	}
	evidence := window.Evidences[0]
	if len(evidence.Jobs) != 2 {
		t.Fatalf("Jobs = %+v, want the two identified jobs", evidence.Jobs)
	}
	if len(evidence.Jobs[0].InlineArtifacts) != 1 || evidence.Jobs[0].InlineArtifacts[0].Name != "ok.zip" {
		t.Errorf("InlineArtifacts = %+v, want only the named artifact", evidence.Jobs[0].InlineArtifacts)
	}
	if !evidence.JobsPartial {
		t.Error("JobsPartial = false, want true from X-Total 4 over 3 jobs")
	}
	reasons := map[string]bool{}
	for _, warning := range evidence.Warnings {
		reasons[warning.Reason] = true
	}
	for _, want := range []string{"job_missing_id", "artifact_missing_id"} {
		if !reasons[want] {
			t.Errorf("Warnings = %+v, want %q", evidence.Warnings, want)
		}
	}

	// A list entry without identity aborts retryable, never unnamed.
	badList := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"name":"no-id"}]`))
	}))
	t.Cleanup(badList.Close)
	_, err = (GitLabClient{HTTPClient: badList.Client()}).FetchWindow(
		context.Background(), gitlabFetchTarget(badList.URL), "fetch-token")
	var retryable *RetryableError
	if !errors.As(err, &retryable) {
		t.Fatalf("FetchWindow(bad id) error = %T, want *RetryableError", err)
	}
}

// A throttled detail read on the first pipeline commits nothing, so the
// halt releases retryable with the provider's guidance instead of
// reporting an unchanged success while throttled.
func TestFetchGitLabDetailThrottleWithNoPrefixIsRetryable(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.EscapedPath() {
		case "/projects/eshu-hq%2Fdemo/pipelines":
			w.Header().Set("X-Total", "1")
			_, _ = w.Write([]byte(gitlabPipelineListPayload()))
		case "/projects/eshu-hq%2Fdemo/pipelines/7700":
			w.Header().Set("Retry-After", "45")
			http.Error(w, "throttled", http.StatusTooManyRequests)
		default:
			t.Errorf("unexpected path %s", r.URL.EscapedPath())
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	_, err := (GitLabClient{HTTPClient: server.Client()}).FetchWindow(
		context.Background(), gitlabFetchTarget(server.URL), "fetch-token")
	var retryable *RetryableError
	if !errors.As(err, &retryable) {
		t.Fatalf("FetchWindow() error = %T, want *RetryableError", err)
	}
	if retryable.RetryAfter != 45*time.Second {
		t.Errorf("RetryAfter = %v, want the provider's 45-second guidance", retryable.RetryAfter)
	}
}
