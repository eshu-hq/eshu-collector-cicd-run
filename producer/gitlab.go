// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"strings"

	"github.com/eshu-hq/eshu-sdk/collector"
	"github.com/eshu-hq/eshu-sdk/factschema"
	cicdrunv1 "github.com/eshu-hq/eshu-sdk/factschema/cicdrun/v1"
	"github.com/eshu-hq/eshu-sdk/factschema/identity"
)

func buildGitLabFacts(ctx emissionContext, provider Provider, evidence Evidence) ([]collector.Fact, error) {
	var facts []collector.Fact
	runFact, err := gitLabRunFact(ctx, provider, evidence)
	if err != nil {
		return nil, err
	}
	facts = append(facts, runFact)
	for _, job := range evidence.Jobs {
		jobFact, err := gitLabJobFact(ctx, provider, evidence, job)
		if err != nil {
			return nil, err
		}
		facts = append(facts, jobFact)
		for _, artifact := range job.InlineArtifacts {
			artifactFact, err := gitLabArtifactFact(ctx, provider, evidence, job, artifact)
			if err != nil {
				return nil, err
			}
			facts = append(facts, artifactFact)
			// GitLab's job artifacts list never carries a content digest,
			// so every emitted artifact is followed by this warning — it
			// is not a fixture gap, it matches the real API shape.
			digestWarning, err := warningFact(ctx, provider, evidence, WarningEvidence{
				Key:     "artifact:" + artifact.ID,
				Reason:  "artifact_missing_digest",
				Message: "artifact metadata did not include a digest",
			})
			if err != nil {
				return nil, err
			}
			facts = append(facts, digestWarning)
		}
	}
	for _, warning := range evidence.Warnings {
		warningFact, err := warningFact(ctx, provider, evidence, warning)
		if err != nil {
			return nil, err
		}
		facts = append(facts, warningFact)
	}
	return facts, nil
}

// sharedPayload mirrors the in-tree sharedPayload/gitlabSharedPayload:
// the collector boundary fields every fact in one claim shares.

func gitLabRunFact(ctx emissionContext, provider Provider, evidence Evidence) (collector.Fact, error) {
	runID := strings.TrimSpace(evidence.RunID)
	payload := sharedPayload(ctx, provider, runID, evidence.RunAttempt)
	// GitLab pipelines model a single terminal "status" enum rather than
	// GitHub's separate status/conclusion pair, so both the "status" and
	// "result" payload keys mirror the pipeline status.
	status := strings.TrimSpace(evidence.Status)
	repositoryURL := gitLabProjectURL(ctx.claim.Scope.ID, evidence.Repository.WebURL)
	payload["run_number"] = strings.TrimSpace(evidence.RunNumber)
	payload["event"] = strings.TrimSpace(evidence.Source)
	payload["status"] = status
	payload["result"] = status
	payload["branch"] = strings.TrimSpace(evidence.Branch)
	payload["commit_sha"] = strings.TrimSpace(evidence.CommitSHA)
	// repository_id: unset pending the SDK canonical-ID helper (see
	// pipelineDefinitionFact). provider_repository_id always emits.
	payload["repository_id"] = ""
	payload["provider_repository_id"] = providerRepositoryID(ctx, provider, evidence)
	payload["repository_url"] = repositoryURL
	payload["actor"] = strings.TrimSpace(evidence.Actor)
	payload["started_at"] = strings.TrimSpace(evidence.StartedAt)
	payload["updated_at"] = strings.TrimSpace(evidence.UpdatedAt)
	redactions := urlRedaction("url", strings.TrimSpace(evidence.Repository.WebURL))
	payload["url"] = stripSensitiveURL(evidence.Repository.WebURL)
	payload["correlation_anchors"] = nonEmptyStrings("", strings.TrimSpace(evidence.CommitSHA), runID)
	if err := mergeContractPayload(payload, func() (map[string]any, error) {
		return factschema.EncodeCICDRun(cicdrunv1.Run{
			Provider:             string(provider),
			RunID:                runID,
			RunAttempt:           stringPtr(payload["run_attempt"].(string)),
			RunNumber:            stringPtr(strings.TrimSpace(evidence.RunNumber)),
			Event:                stringPtr(strings.TrimSpace(evidence.Source)),
			Status:               stringPtr(status),
			Result:               stringPtr(status),
			Branch:               stringPtr(strings.TrimSpace(evidence.Branch)),
			CommitSHA:            stringPtr(strings.TrimSpace(evidence.CommitSHA)),
			ProviderRepositoryID: stringPtr(providerRepositoryID(ctx, provider, evidence)),
			RepositoryURL:        stringPtr(repositoryURL),
			Actor:                stringPtr(strings.TrimSpace(evidence.Actor)),
			StartedAt:            stringPtr(strings.TrimSpace(evidence.StartedAt)),
			UpdatedAt:            stringPtr(strings.TrimSpace(evidence.UpdatedAt)),
			URL:                  stringPtr(stripSensitiveURL(evidence.Repository.WebURL)),
			CorrelationAnchors:   nonEmptyStrings("", strings.TrimSpace(evidence.CommitSHA), runID),
			CollectorInstanceID:  stringPtr(ctx.collectorInstance),
		})
	}); err != nil {
		return collector.Fact{}, err
	}
	stableKey := identity.StableID(KindRun, map[string]any{
		"provider":    string(provider),
		"run_attempt": payload["run_attempt"],
		"run_id":      runID,
	})
	return newFact(ctx, KindRun, stableKey, runID, payload, redactions), nil
}

func gitLabJobFact(ctx emissionContext, provider Provider, evidence Evidence, job JobEvidence) (collector.Fact, error) {
	jobID := strings.TrimSpace(job.ID)
	payload := sharedPayload(ctx, provider, strings.TrimSpace(evidence.RunID), evidence.RunAttempt)
	payload["job_id"] = jobID
	payload["job_name"] = strings.TrimSpace(job.Name)
	payload["stage"] = strings.TrimSpace(job.Stage)
	payload["status"] = strings.TrimSpace(job.Status)
	payload["result"] = strings.TrimSpace(job.Status)
	payload["started_at"] = strings.TrimSpace(job.StartedAt)
	payload["completed_at"] = strings.TrimSpace(job.CompletedAt)
	stableKey := identity.StableID(KindJob, map[string]any{
		"job_id":      jobID,
		"run_attempt": payload["run_attempt"],
		"run_id":      payload["run_id"],
	})
	return newFact(ctx, KindJob, stableKey, jobID, payload, nil), nil
}

func gitLabArtifactFact(ctx emissionContext, provider Provider, evidence Evidence, job JobEvidence, artifact ArtifactEvidence) (collector.Fact, error) {
	jobID := strings.TrimSpace(job.ID)
	artifactID := strings.TrimSpace(artifact.ID)
	artifactType := strings.TrimSpace(artifact.Type)
	if artifactType == "" {
		artifactType = "generic"
	}
	payload := sharedPayload(ctx, provider, strings.TrimSpace(evidence.RunID), evidence.RunAttempt)
	payload["job_id"] = jobID
	payload["artifact_id"] = artifactID
	payload["artifact_name"] = strings.TrimSpace(artifact.Name)
	payload["artifact_type"] = artifactType
	payload["artifact_digest"] = ""
	payload["size_bytes"] = artifact.SizeBytes
	payload["correlation_anchors"] = nonEmptyStrings(payload["run_id"].(string), jobID)
	if err := mergeContractPayload(payload, func() (map[string]any, error) {
		return factschema.EncodeCICDArtifact(cicdrunv1.Artifact{
			Provider:            string(provider),
			RunID:               payload["run_id"].(string),
			RunAttempt:          stringPtr(payload["run_attempt"].(string)),
			ArtifactID:          stringPtr(artifactID),
			ArtifactName:        stringPtr(strings.TrimSpace(artifact.Name)),
			ArtifactType:        stringPtr(artifactType),
			ArtifactDigest:      stringPtr(""),
			SizeBytes:           int64Ptr(artifact.SizeBytes),
			CorrelationAnchors:  nonEmptyStrings(payload["run_id"].(string), jobID),
			CollectorInstanceID: stringPtr(ctx.collectorInstance),
		})
	}); err != nil {
		return collector.Fact{}, err
	}
	stableKey := identity.StableID(KindArtifact, map[string]any{
		"artifact_id": artifactID,
		"run_attempt": payload["run_attempt"],
		"run_id":      payload["run_id"],
	})
	return newFact(ctx, KindArtifact, stableKey, artifactID, payload, nil), nil
}

// urlRedaction records a redaction entry when sanitization removed
// material from a URL field. The SDK requires non-blank field and reason.
