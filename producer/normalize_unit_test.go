// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"encoding/json"
	"testing"
)

func normalizeTestRun() map[string]any {
	return map[string]any{
		"id":             json.Number("1001"),
		"run_number":     json.Number("42"),
		"run_attempt":    json.Number("1"),
		"repository":     map[string]any{"full_name": "example-org/example-repo"},
		"head_sha":       "0123456789abcdef0123456789abcdef01234567",
		"name":           "CI",
		"event":          "push",
		"status":         "completed",
		"conclusion":     "success",
		"head_branch":    "main",
		"html_url":       "https://github.com/example-org/example-repo/actions/runs/1001",
		"run_started_at": "2026-10-27T00:00:00Z",
		"updated_at":     "2026-10-27T00:05:00Z",
		"actor":          map[string]any{"login": "octocat"},
	}
}

func TestNormalizeGitHubRunRejectsBadIdentity(t *testing.T) {
	t.Parallel()

	badNumber := normalizeTestRun()
	badNumber["run_number"] = float64(4.5)
	if _, err := normalizeGitHubRun(badNumber); err == nil {
		t.Error("normalizeGitHubRun(bad run_number) = nil, want rejection")
	}
	badAttempt := normalizeTestRun()
	badAttempt["run_attempt"] = true
	if _, err := normalizeGitHubRun(badAttempt); err == nil {
		t.Error("normalizeGitHubRun(bad run_attempt) = nil, want rejection")
	}
}

func TestNormalizeGitHubRunWarnsOnMissingAnchors(t *testing.T) {
	t.Parallel()

	run := normalizeTestRun()
	delete(run, "repository")
	delete(run, "head_sha")
	evidence, err := normalizeGitHubRun(run)
	if err != nil {
		t.Fatalf("normalizeGitHubRun() error = %v, want the lenient warning path", err)
	}
	found := false
	for _, warning := range evidence.Warnings {
		if warning.Reason == "run_missing_repository_or_commit" {
			found = true
		}
	}
	if !found {
		t.Errorf("normalizeGitHubRun() warnings = %v, want the missing-anchors warning", evidence.Warnings)
	}
}

func TestNormalizeGitHubJobsDegradesWithoutIdentity(t *testing.T) {
	t.Parallel()

	evidence := windowRunEvidence("1001")
	normalizeGitHubJobs(&evidence, []map[string]any{{"name": "nameless"}}, false)
	found := false
	for _, warning := range evidence.Warnings {
		if warning.Reason == "job_missing_id" {
			found = true
		}
	}
	if !found {
		t.Errorf("normalizeGitHubJobs() warnings = %v, want the job-missing-id warning", evidence.Warnings)
	}
}

func TestNormalizeGitHubJobsSkipsNonMapSteps(t *testing.T) {
	t.Parallel()

	evidence := windowRunEvidence("1001")
	normalizeGitHubJobs(&evidence, []map[string]any{{
		"id":    json.Number("9001"),
		"name":  "build",
		"steps": []any{"not-a-step", map[string]any{"name": "unnumbered"}},
	}}, false)
	if len(evidence.Jobs) != 1 || len(evidence.Jobs[0].Steps) != 0 {
		t.Errorf("normalizeGitHubJobs() jobs = %+v, want one job with no steps", evidence.Jobs)
	}
	found := false
	for _, warning := range evidence.Warnings {
		if warning.Reason == "step_missing_number" {
			found = true
		}
	}
	if !found {
		t.Errorf("normalizeGitHubJobs() warnings = %v, want the step-missing-number warning", evidence.Warnings)
	}
}

func TestArtifactMatchesRunMapMatrix(t *testing.T) {
	t.Parallel()

	run := map[string]any{"id": json.Number("1001"), "head_sha": "abc"}
	if !artifactMatchesRunMap(run, map[string]any{"name": "orphan"}) {
		t.Error("artifactMatchesRunMap(no workflow_run ref) = false, want true")
	}
	foreign := map[string]any{"workflow_run": map[string]any{"id": json.Number("9999")}}
	if artifactMatchesRunMap(run, foreign) {
		t.Error("artifactMatchesRunMap(other run) = true, want false")
	}
}

func TestNormalizeGitHubDeploymentWithoutStatuses(t *testing.T) {
	t.Parallel()

	rows, err := normalizeGitHubDeployment(map[string]any{
		"id": json.Number("7001"), "environment": "production",
	}, nil)
	if err != nil {
		t.Fatalf("normalizeGitHubDeployment() error = %v, want nil", err)
	}
	if len(rows) != 1 || rows[0].ID != "7001" || rows[0].Environment != "production" {
		t.Errorf("normalizeGitHubDeployment() = %+v, want the single status-less row", rows)
	}
}

func TestAnyInt64Matrix(t *testing.T) {
	t.Parallel()

	if got := anyInt64(int64(7700)); got != 7700 {
		t.Errorf("anyInt64(int64) = %d, want passthrough", got)
	}
	if got := anyInt64("7700"); got != 0 {
		t.Errorf("anyInt64(string) = %d, want zero for the non-integer", got)
	}
}
