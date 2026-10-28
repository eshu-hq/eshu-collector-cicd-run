// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// normalizeGitHubRun maps one workflow-run object to the Evidence seam.
// Identity fields (id, run number, attempt) are strict: a provider run
// without identity is a malformed payload, not an emission. All other
// fields stay lenient exactly like the in-tree fixture normalizer.
func normalizeGitHubRun(run map[string]any) (Evidence, error) {
	runID, err := numericProviderID(run["id"])
	if err != nil || strings.TrimSpace(runID) == "" {
		return Evidence{}, fmt.Errorf("github actions run.id: %v", run["id"])
	}
	runNumber, err := numericProviderID(run["run_number"])
	if err != nil {
		return Evidence{}, fmt.Errorf("github actions run.run_number: %v", run["run_number"])
	}
	attempt, err := numericProviderID(run["run_attempt"])
	if err != nil {
		return Evidence{}, fmt.Errorf("github actions run.run_attempt: %v", run["run_attempt"])
	}
	var repository map[string]any
	if raw, ok := run["repository"].(map[string]any); ok {
		repository = raw
	}
	var actor map[string]any
	if raw, ok := run["actor"].(map[string]any); ok {
		actor = raw
	}
	evidence := Evidence{
		Provider:   string(ProviderGitHubActions),
		RunID:      runID,
		RunAttempt: attempt,
		Repository: RepositoryEvidence{
			FullName: anyString(repository["full_name"]),
			HTMLURL:  anyString(repository["html_url"]),
		},
		RunName:   anyString(run["name"]),
		RunNumber: runNumber,
		Event:     anyString(run["event"]),
		Status:    anyString(run["status"]),
		Branch:    anyString(run["head_branch"]),
		CommitSHA: anyString(run["head_sha"]),
		RunURL:    anyString(run["html_url"]),
		StartedAt: anyString(run["run_started_at"]),
		UpdatedAt: anyString(run["updated_at"]),
		Actor:     anyString(actor["login"]),
	}
	evidence.Conclusion = anyString(run["conclusion"])
	workflowID, _ := numericProviderID(run["workflow_id"])
	if workflowID != "" || anyString(run["path"]) != "" || anyString(run["name"]) != "" {
		evidence.Workflow = &WorkflowEvidence{
			ID:   workflowID,
			Name: anyString(run["name"]),
			Path: anyString(run["path"]),
		}
	}
	if evidence.Repository.FullName == "" || evidence.CommitSHA == "" {
		evidence.Warnings = append(evidence.Warnings, WarningEvidence{
			Key:     "run:anchors",
			Reason:  "run_missing_repository_or_commit",
			Message: "run metadata omitted repository locator or commit SHA",
		})
	}
	return evidence, nil
}

// normalizeGitHubJobs appends one run's bounded jobs to its evidence. Jobs
// without provider identity degrade to an explicit warning (mirroring the
// in-tree fixture normalizer) rather than dropping the run; steps without
// a number degrade the same way.
func normalizeGitHubJobs(evidence *Evidence, jobs []map[string]any, partial bool) {
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
		var labels []string
		if raw, ok := job["labels"].([]any); ok {
			for _, label := range raw {
				if text, ok := label.(string); ok && strings.TrimSpace(text) != "" {
					labels = append(labels, strings.TrimSpace(text))
				}
			}
		}
		entry := JobEvidence{
			ID:          jobID,
			Name:        anyString(job["name"]),
			Status:      anyString(job["status"]),
			Conclusion:  anyString(job["conclusion"]),
			StartedAt:   anyString(job["started_at"]),
			CompletedAt: anyString(job["completed_at"]),
			Labels:      labels,
		}
		if raw, ok := job["steps"].([]any); ok {
			for _, rawStep := range raw {
				step, ok := rawStep.(map[string]any)
				if !ok {
					continue
				}
				number, err := numericProviderID(step["number"])
				if err != nil || strings.TrimSpace(number) == "" {
					evidence.Warnings = append(evidence.Warnings, WarningEvidence{
						Key:     "step:" + jobID + ":" + anyString(step["name"]),
						Reason:  "step_missing_number",
						Message: "step metadata omitted provider step number",
					})
					continue
				}
				entry.Steps = append(entry.Steps, StepEvidence{
					Number:     number,
					Name:       anyString(step["name"]),
					Status:     anyString(step["status"]),
					Conclusion: anyString(step["conclusion"]),
					StartedAt:  anyString(step["started_at"]),
					Completed:  anyString(step["completed_at"]),
				})
			}
		}
		evidence.Jobs = append(evidence.Jobs, entry)
	}
}

// normalizeGitHubArtifacts appends one run's bounded artifacts to its
// evidence. The archive URL is query-stripped at the seam (porting the
// in-tree sanitizeArtifacts): the builder drops query-bearing URLs
// outright, so persisting the raw signed URL would lose every download
// URL. Artifacts that do not reference this run degrade to an explicit
// warning; a missing digest degrades the same way while still emitting,
// matching the real API shape where the Artifacts API carries no digest.
func normalizeGitHubArtifacts(evidence *Evidence, run map[string]any, artifacts []map[string]any, partial bool) {
	evidence.ArtifactsPartial = partial
	for index, artifact := range artifacts {
		artifactID, err := numericProviderID(artifact["id"])
		if err != nil || strings.TrimSpace(artifactID) == "" {
			evidence.Warnings = append(evidence.Warnings, WarningEvidence{
				Key:     fmt.Sprintf("artifact:%d", index),
				Reason:  "artifact_missing_id",
				Message: "artifact metadata omitted provider artifact ID",
			})
			continue
		}
		if !artifactMatchesRunMap(run, artifact) {
			evidence.Warnings = append(evidence.Warnings, WarningEvidence{
				Key:     "artifact:" + artifactID,
				Reason:  "artifact_run_mismatch",
				Message: "artifact workflow_run reference did not match enclosing run",
			})
			continue
		}
		evidence.Artifacts = append(evidence.Artifacts, ArtifactEvidence{
			ID:          artifactID,
			Name:        anyString(artifact["name"]),
			Type:        anyString(artifact["artifact_type"]),
			Digest:      anyString(artifact["digest"]),
			SizeBytes:   anyInt64(artifact["size_in_bytes"]),
			DownloadURL: stripURLQuery(anyString(artifact["archive_download_url"])),
			CreatedAt:   anyString(artifact["created_at"]),
			ExpiresAt:   anyString(artifact["expires_at"]),
			Expired:     anyBool(artifact["expired"]),
		})
		if anyString(artifact["digest"]) == "" {
			evidence.Warnings = append(evidence.Warnings, WarningEvidence{
				Key:     "artifact:" + artifactID,
				Reason:  "artifact_missing_digest",
				Message: "artifact metadata did not include a digest",
			})
		}
	}
}

// artifactMatchesRunMap ports the in-tree artifactMatchesRun guard: an
// artifact whose workflow_run reference names a different run or head SHA
// does not belong to this run's evidence.
func artifactMatchesRunMap(run, artifact map[string]any) bool {
	var ref map[string]any
	if raw, ok := artifact["workflow_run"].(map[string]any); ok {
		ref = raw
	} else {
		return true
	}
	if ref["id"] != nil {
		artifactRunID, err := numericProviderID(ref["id"])
		runID, runErr := numericProviderID(run["id"])
		if err != nil || runErr != nil {
			return false
		}
		if artifactRunID != "" && artifactRunID != runID {
			return false
		}
	}
	artifactSHA := anyString(ref["head_sha"])
	runSHA := anyString(run["head_sha"])
	return artifactSHA == "" || runSHA == "" || artifactSHA == runSHA
}

// normalizeGitHubDeployment maps one deployment plus its bounded status
// window to one DeploymentEvidence row per status (or one status-less row
// when no statuses were fetched). The status value wins over the parent
// deployment wherever both name a field, mirroring the in-tree
// deploymentEventEnvelope; URLs are query-stripped at the seam because
// they can carry signed material.
func normalizeGitHubDeployment(deployment map[string]any, statuses []map[string]any) ([]DeploymentEvidence, error) {
	deploymentID, err := numericProviderID(deployment["id"])
	if err != nil {
		return nil, fmt.Errorf("github deployment.id: %v", deployment["id"])
	}
	parent := DeploymentEvidence{
		ID:                    deploymentID,
		Environment:           anyString(deployment["environment"]),
		SHA:                   anyString(deployment["sha"]),
		Ref:                   anyString(deployment["ref"]),
		Task:                  anyString(deployment["task"]),
		OriginalEnvironment:   anyString(deployment["original_environment"]),
		Description:           anyString(deployment["description"]),
		CreatedAt:             anyString(deployment["created_at"]),
		UpdatedAt:             anyString(deployment["updated_at"]),
		ProductionEnvironment: anyBool(deployment["production_environment"]),
		TransientEnvironment:  anyBool(deployment["transient_environment"]),
	}
	if len(statuses) == 0 {
		return []DeploymentEvidence{parent}, nil
	}
	rows := make([]DeploymentEvidence, 0, len(statuses))
	for _, raw := range statuses {
		statusID, err := numericProviderID(raw["id"])
		if err != nil {
			return nil, fmt.Errorf("github deployment status.id: %v", raw["id"])
		}
		row := parent
		row.StatusID = statusID
		row.State = anyString(raw["state"])
		if anyString(raw["description"]) != "" {
			row.Description = anyString(raw["description"])
		}
		if anyString(raw["created_at"]) != "" {
			row.CreatedAt = anyString(raw["created_at"])
		}
		if anyString(raw["updated_at"]) != "" {
			row.UpdatedAt = anyString(raw["updated_at"])
		}
		if anyString(raw["environment"]) != "" {
			row.Environment = anyString(raw["environment"])
		}
		row.EnvironmentURL = stripURLQuery(anyString(raw["environment_url"]))
		row.LogURL = stripURLQuery(anyString(raw["log_url"]))
		rows = append(rows, row)
	}
	return rows, nil
}

// stripURLQuery drops the query string and fragment from a provider URL,
// porting the in-tree sanitizeArtifacts/stripURLQuery treatment for
// artifact download URLs. An unparseable value yields "" rather than
// passing token-bearing material through.
func stripURLQuery(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return ""
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

// anyInt64 renders an optional provider integer (json.Number from a
// UseNumber decode, float64, or int variants) into int64. Non-integers
// become zero rather than failing the run.
func anyInt64(value any) int64 {
	switch typed := value.(type) {
	case json.Number:
		if number, err := typed.Int64(); err == nil {
			return number
		}
	case float64:
		return int64(typed)
	case int:
		return int64(typed)
	case int64:
		return typed
	}
	return 0
}

// anyBool renders an optional provider boolean. Non-booleans become false.
func anyBool(value any) bool {
	boolean, _ := value.(bool)
	return boolean
}
