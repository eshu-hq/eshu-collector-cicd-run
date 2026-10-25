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

func buildGitHubFacts(ctx emissionContext, provider Provider, evidence Evidence) ([]collector.Fact, error) {
	var facts []collector.Fact
	if evidence.Workflow != nil {
		fact, err := pipelineDefinitionFact(ctx, provider, evidence)
		if err != nil {
			return nil, err
		}
		facts = append(facts, fact)
	}
	runFact, err := runFact(ctx, provider, evidence)
	if err != nil {
		return nil, err
	}
	facts = append(facts, runFact)
	for _, job := range evidence.Jobs {
		jobFact, err := jobFact(ctx, provider, evidence, job)
		if err != nil {
			return nil, err
		}
		facts = append(facts, jobFact)
		for _, step := range job.Steps {
			stepFact, err := stepFact(ctx, provider, evidence, job, step)
			if err != nil {
				return nil, err
			}
			facts = append(facts, stepFact)
		}
		if strings.TrimSpace(job.Environment) != "" {
			envFact, err := environmentFact(ctx, provider, evidence, job)
			if err != nil {
				return nil, err
			}
			facts = append(facts, envFact)
		}
	}
	for _, artifact := range evidence.Artifacts {
		artifactFact, err := gitHubArtifactFact(ctx, provider, evidence, artifact)
		if err != nil {
			return nil, err
		}
		facts = append(facts, artifactFact)
	}
	for _, trigger := range evidence.Triggers {
		if strings.TrimSpace(trigger.SourceRunID) == "" || strings.TrimSpace(trigger.SourceProvider) == "" || strings.TrimSpace(trigger.Kind) == "" {
			warning, err := warningFact(ctx, provider, evidence, WarningEvidence{
				Key:     "trigger:unanchored",
				Reason:  "trigger_edge_missing_anchor",
				Message: "trigger edge omitted source provider, source run ID, or trigger kind",
			})
			if err != nil {
				return nil, err
			}
			facts = append(facts, warning)
			continue
		}
		triggerFact, err := triggerFact(ctx, provider, evidence, trigger)
		if err != nil {
			return nil, err
		}
		facts = append(facts, triggerFact)
	}
	for _, deployment := range evidence.Deployments {
		deploymentFact, err := deploymentFact(ctx, provider, evidence, deployment)
		if err != nil {
			return nil, err
		}
		facts = append(facts, deploymentFact)
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

func actionReference(stepName string) string {
	stepName = strings.TrimPrefix(strings.TrimSpace(stepName), "Run ")
	if strings.Contains(stepName, "@") && !strings.Contains(stepName, " ") {
		return stepName
	}
	return ""
}

// pipelineDefinitionFact builds ci.pipeline_definition (GitHub only).
//
// OPEN GAP (P1): the stable-key identity and payload carry the canonical
// repository_id, which the in-tree producer derives via the core-owned
// repositoryidentity.CanonicalRepositoryID
// (go/internal/repositoryidentity, consulted in
// go/internal/collector/cicdrun/github_actions_helpers.go:repositoryID).
// The published SDK exports no canonical repository-ID helper, and
// reimplementing the hash here would mint a divergent id and silently
// break the backbone join with the git collector — worse than absent.
// The port therefore emits the raw provider_repository_id provenance and
// leaves repository_id unset pending an SDK helper. The reducer treats a
// missing repository_id as UNRESOLVED (matcher-level gate), never as a
// decode error, so this degrades to no false joins rather than wrong
// joins.

// pipelineDefinitionFact builds ci.pipeline_definition (GitHub only).
//
// OPEN GAP (P1): the stable-key identity and payload carry the canonical
// repository_id, which the in-tree producer derives via the core-owned
// repositoryidentity.CanonicalRepositoryID
// (go/internal/repositoryidentity, consulted in
// go/internal/collector/cicdrun/github_actions_helpers.go:repositoryID).
// The published SDK exports no canonical repository-ID helper, and
// reimplementing the hash here would mint a divergent id and silently
// break the backbone join with the git collector — worse than absent.
// The port therefore emits the raw provider_repository_id provenance and
// leaves repository_id unset pending an SDK helper. The reducer treats a
// missing repository_id as UNRESOLVED (matcher-level gate), never as a
// decode error, so this degrades to no false joins rather than wrong
// joins.
func pipelineDefinitionFact(ctx emissionContext, provider Provider, evidence Evidence) (collector.Fact, error) {
	workflow := evidence.Workflow
	payload := sharedPayload(ctx, provider, strings.TrimSpace(evidence.RunID), evidence.RunAttempt)
	payload["workflow_id"] = strings.TrimSpace(workflow.ID)
	payload["workflow_name"] = strings.TrimSpace(workflow.Name)
	payload["workflow_path"] = strings.TrimSpace(workflow.Path)
	payload["workflow_state"] = strings.TrimSpace(workflow.State)
	payload["trigger"] = strings.TrimSpace(workflow.Trigger)
	payload["repository_id"] = ""
	payload["provider_repository_id"] = providerRepositoryID(ctx, provider, evidence)
	payload["definition_kind"] = "workflow"
	stableKey := identity.StableID(KindPipelineDefinition, map[string]any{
		"provider":      string(provider),
		"repository_id": payload["repository_id"],
		"workflow_id":   payload["workflow_id"],
		"workflow_path": payload["workflow_path"],
	})
	return newFact(ctx, KindPipelineDefinition, stableKey, strings.TrimSpace(workflow.ID), payload, nil), nil
}

func runFact(ctx emissionContext, provider Provider, evidence Evidence) (collector.Fact, error) {
	runID := strings.TrimSpace(evidence.RunID)
	payload := sharedPayload(ctx, provider, runID, evidence.RunAttempt)
	payload["run_number"] = strings.TrimSpace(evidence.RunNumber)
	payload["workflow_name"] = strings.TrimSpace(evidence.RunName)
	payload["event"] = strings.TrimSpace(evidence.Event)
	payload["status"] = strings.TrimSpace(evidence.Status)
	payload["result"] = strings.TrimSpace(evidence.Conclusion)
	payload["branch"] = strings.TrimSpace(evidence.Branch)
	payload["commit_sha"] = strings.TrimSpace(evidence.CommitSHA)
	// repository_id: unset pending the SDK canonical-ID helper (see
	// pipelineDefinitionFact). provider_repository_id always emits.
	payload["repository_id"] = ""
	payload["provider_repository_id"] = providerRepositoryID(ctx, provider, evidence)
	payload["repository_url"] = strings.TrimSpace(evidence.Repository.HTMLURL)
	payload["actor"] = strings.TrimSpace(evidence.Actor)
	payload["started_at"] = strings.TrimSpace(evidence.StartedAt)
	payload["updated_at"] = strings.TrimSpace(evidence.UpdatedAt)
	redactions := urlRedaction("url", strings.TrimSpace(evidence.RunURL))
	payload["url"] = stripSensitiveURL(evidence.RunURL)
	payload["correlation_anchors"] = nonEmptyStrings("", strings.TrimSpace(evidence.CommitSHA), runID)
	if err := mergeContractPayload(payload, func() (map[string]any, error) {
		return factschema.EncodeCICDRun(cicdrunv1.Run{
			Provider:             string(provider),
			RunID:                runID,
			RunAttempt:           stringPtr(payload["run_attempt"].(string)),
			RunNumber:            stringPtr(strings.TrimSpace(evidence.RunNumber)),
			WorkflowName:         stringPtr(strings.TrimSpace(evidence.RunName)),
			Event:                stringPtr(strings.TrimSpace(evidence.Event)),
			Status:               stringPtr(strings.TrimSpace(evidence.Status)),
			Result:               stringPtr(strings.TrimSpace(evidence.Conclusion)),
			Branch:               stringPtr(strings.TrimSpace(evidence.Branch)),
			CommitSHA:            stringPtr(strings.TrimSpace(evidence.CommitSHA)),
			ProviderRepositoryID: stringPtr(providerRepositoryID(ctx, provider, evidence)),
			RepositoryURL:        stringPtr(strings.TrimSpace(evidence.Repository.HTMLURL)),
			Actor:                stringPtr(strings.TrimSpace(evidence.Actor)),
			StartedAt:            stringPtr(strings.TrimSpace(evidence.StartedAt)),
			UpdatedAt:            stringPtr(strings.TrimSpace(evidence.UpdatedAt)),
			URL:                  stringPtr(stripSensitiveURL(evidence.RunURL)),
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

func jobFact(ctx emissionContext, provider Provider, evidence Evidence, job JobEvidence) (collector.Fact, error) {
	jobID := strings.TrimSpace(job.ID)
	payload := sharedPayload(ctx, provider, strings.TrimSpace(evidence.RunID), evidence.RunAttempt)
	payload["job_id"] = jobID
	payload["job_name"] = strings.TrimSpace(job.Name)
	payload["status"] = strings.TrimSpace(job.Status)
	payload["result"] = strings.TrimSpace(job.Conclusion)
	payload["started_at"] = strings.TrimSpace(job.StartedAt)
	payload["completed_at"] = strings.TrimSpace(job.CompletedAt)
	payload["runner_labels"] = append([]string(nil), job.Labels...)
	stableKey := identity.StableID(KindJob, map[string]any{
		"job_id":      jobID,
		"run_attempt": payload["run_attempt"],
		"run_id":      payload["run_id"],
	})
	return newFact(ctx, KindJob, stableKey, jobID, payload, nil), nil
}

func stepFact(ctx emissionContext, provider Provider, evidence Evidence, job JobEvidence, step StepEvidence) (collector.Fact, error) {
	stepNumber := strings.TrimSpace(step.Number)
	jobID := strings.TrimSpace(job.ID)
	payload := sharedPayload(ctx, provider, strings.TrimSpace(evidence.RunID), evidence.RunAttempt)
	payload["job_id"] = jobID
	payload["step_number"] = stepNumber
	payload["step_name"] = strings.TrimSpace(step.Name)
	payload["status"] = strings.TrimSpace(step.Status)
	payload["result"] = strings.TrimSpace(step.Conclusion)
	payload["started_at"] = strings.TrimSpace(step.StartedAt)
	payload["completed_at"] = strings.TrimSpace(step.Completed)
	payload["action_ref"] = actionReference(step.Name)
	if err := mergeContractPayload(payload, func() (map[string]any, error) {
		return factschema.EncodeCICDStep(cicdrunv1.Step{
			Provider:            string(provider),
			RunID:               payload["run_id"].(string),
			RunAttempt:          stringPtr(payload["run_attempt"].(string)),
			JobID:               stringPtr(jobID),
			StepNumber:          stringPtr(stepNumber),
			StepName:            stringPtr(strings.TrimSpace(step.Name)),
			Status:              stringPtr(strings.TrimSpace(step.Status)),
			Result:              stringPtr(strings.TrimSpace(step.Conclusion)),
			StartedAt:           stringPtr(strings.TrimSpace(step.StartedAt)),
			CompletedAt:         stringPtr(strings.TrimSpace(step.Completed)),
			ActionRef:           stringPtr(actionReference(step.Name)),
			CollectorInstanceID: stringPtr(ctx.collectorInstance),
		})
	}); err != nil {
		return collector.Fact{}, err
	}
	stableKey := identity.StableID(KindStep, map[string]any{
		"job_id":      jobID,
		"run_attempt": payload["run_attempt"],
		"run_id":      payload["run_id"],
		"step_number": stepNumber,
	})
	return newFact(ctx, KindStep, stableKey, jobID+":"+stepNumber, payload, nil), nil
}

func gitHubArtifactFact(ctx emissionContext, provider Provider, evidence Evidence, artifact ArtifactEvidence) (collector.Fact, error) {
	artifactID := strings.TrimSpace(artifact.ID)
	artifactType := strings.TrimSpace(artifact.Type)
	if artifactType == "" {
		artifactType = "generic"
	}
	payload := sharedPayload(ctx, provider, strings.TrimSpace(evidence.RunID), evidence.RunAttempt)
	payload["artifact_id"] = artifactID
	payload["artifact_name"] = strings.TrimSpace(artifact.Name)
	payload["artifact_type"] = artifactType
	payload["artifact_digest"] = strings.TrimSpace(artifact.Digest)
	payload["size_bytes"] = artifact.SizeBytes
	payload["expired"] = artifact.Expired
	payload["created_at"] = strings.TrimSpace(artifact.CreatedAt)
	payload["expires_at"] = strings.TrimSpace(artifact.ExpiresAt)
	redactions := urlRedaction("download_url", strings.TrimSpace(artifact.DownloadURL))
	payload["download_url"] = stripSensitiveURL(artifact.DownloadURL)
	payload["correlation_anchors"] = nonEmptyStrings(payload["run_id"].(string), strings.TrimSpace(artifact.Digest))
	if err := mergeContractPayload(payload, func() (map[string]any, error) {
		return factschema.EncodeCICDArtifact(cicdrunv1.Artifact{
			Provider:            string(provider),
			RunID:               payload["run_id"].(string),
			RunAttempt:          stringPtr(payload["run_attempt"].(string)),
			ArtifactID:          stringPtr(artifactID),
			ArtifactName:        stringPtr(strings.TrimSpace(artifact.Name)),
			ArtifactType:        stringPtr(artifactType),
			ArtifactDigest:      stringPtr(strings.TrimSpace(artifact.Digest)),
			SizeBytes:           int64Ptr(artifact.SizeBytes),
			Expired:             boolPtr(artifact.Expired),
			CreatedAt:           stringPtr(strings.TrimSpace(artifact.CreatedAt)),
			ExpiresAt:           stringPtr(strings.TrimSpace(artifact.ExpiresAt)),
			DownloadURL:         stringPtr(stripSensitiveURL(artifact.DownloadURL)),
			CorrelationAnchors:  nonEmptyStrings(payload["run_id"].(string), strings.TrimSpace(artifact.Digest)),
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
	return newFact(ctx, KindArtifact, stableKey, artifactID, payload, redactions), nil
}

func environmentFact(ctx emissionContext, provider Provider, evidence Evidence, job JobEvidence) (collector.Fact, error) {
	jobID := strings.TrimSpace(job.ID)
	payload := sharedPayload(ctx, provider, strings.TrimSpace(evidence.RunID), evidence.RunAttempt)
	payload["job_id"] = jobID
	payload["environment"] = strings.TrimSpace(job.Environment)
	payload["deployment_status"] = strings.TrimSpace(job.DeploymentStatus)
	if err := mergeContractPayload(payload, func() (map[string]any, error) {
		return factschema.EncodeCICDEnvironmentObservation(cicdrunv1.EnvironmentObservation{
			Provider:            string(provider),
			RunID:               payload["run_id"].(string),
			RunAttempt:          stringPtr(payload["run_attempt"].(string)),
			JobID:               stringPtr(jobID),
			Environment:         stringPtr(strings.TrimSpace(job.Environment)),
			DeploymentStatus:    stringPtr(strings.TrimSpace(job.DeploymentStatus)),
			CollectorInstanceID: stringPtr(ctx.collectorInstance),
		})
	}); err != nil {
		return collector.Fact{}, err
	}
	stableKey := identity.StableID(KindEnvironmentObservation, map[string]any{
		"environment": strings.TrimSpace(job.Environment),
		"job_id":      jobID,
		"run_attempt": payload["run_attempt"],
		"run_id":      payload["run_id"],
	})
	return newFact(ctx, KindEnvironmentObservation, stableKey, jobID+":"+strings.TrimSpace(job.Environment), payload, nil), nil
}

func triggerFact(ctx emissionContext, provider Provider, evidence Evidence, trigger TriggerEvidence) (collector.Fact, error) {
	sourceRunID := strings.TrimSpace(trigger.SourceRunID)
	payload := sharedPayload(ctx, provider, strings.TrimSpace(evidence.RunID), evidence.RunAttempt)
	payload["trigger_kind"] = strings.TrimSpace(trigger.Kind)
	payload["source_provider"] = strings.TrimSpace(trigger.SourceProvider)
	payload["source_run_id"] = sourceRunID
	if err := mergeContractPayload(payload, func() (map[string]any, error) {
		return factschema.EncodeCICDTriggerEdge(cicdrunv1.TriggerEdge{
			Provider:            string(provider),
			RunID:               payload["run_id"].(string),
			RunAttempt:          stringPtr(payload["run_attempt"].(string)),
			TriggerKind:         stringPtr(strings.TrimSpace(trigger.Kind)),
			SourceProvider:      stringPtr(strings.TrimSpace(trigger.SourceProvider)),
			SourceRunID:         stringPtr(sourceRunID),
			CollectorInstanceID: stringPtr(ctx.collectorInstance),
		})
	}); err != nil {
		return collector.Fact{}, err
	}
	stableKey := identity.StableID(KindTriggerEdge, map[string]any{
		"run_attempt":     payload["run_attempt"],
		"run_id":          payload["run_id"],
		"source_provider": strings.TrimSpace(trigger.SourceProvider),
		"source_run_id":   sourceRunID,
		"trigger_kind":    strings.TrimSpace(trigger.Kind),
	})
	return newFact(ctx, KindTriggerEdge, stableKey, sourceRunID, payload, nil), nil
}

func deploymentFact(ctx emissionContext, provider Provider, evidence Evidence, deployment DeploymentEvidence) (collector.Fact, error) {
	deploymentID := strings.TrimSpace(deployment.ID)
	statusID := strings.TrimSpace(deployment.StatusID)
	environmentURL := sanitizeDeploymentURL(deployment.EnvironmentURL)
	logURL := sanitizeDeploymentURL(deployment.LogURL)
	redactions := append(
		urlRedaction("environment_url", strings.TrimSpace(deployment.EnvironmentURL)),
		urlRedaction("log_url", strings.TrimSpace(deployment.LogURL))...,
	)
	payload := map[string]any{
		"collector_instance_id":  ctx.collectorInstance,
		"provider":               string(provider),
		"deployment_id":          deploymentID,
		"environment":            strings.TrimSpace(deployment.Environment),
		"sha":                    strings.TrimSpace(deployment.SHA),
		"status_id":              statusID,
		"ref":                    strings.TrimSpace(deployment.Ref),
		"task":                   strings.TrimSpace(deployment.Task),
		"state":                  strings.TrimSpace(deployment.State),
		"original_environment":   "",
		"description":            "",
		"created_at":             "",
		"updated_at":             "",
		"environment_url":        environmentURL,
		"log_url":                logURL,
		"production_environment": false,
		"transient_environment":  false,
		// repository_id: unset pending the SDK canonical-ID helper (see
		// pipelineDefinitionFact).
		"repository_id": "",
	}
	if err := mergeContractPayload(payload, func() (map[string]any, error) {
		return factschema.EncodeCICDDeploymentEvent(cicdrunv1.DeploymentEvent{
			Provider:       string(provider),
			DeploymentID:   deploymentID,
			Environment:    strings.TrimSpace(deployment.Environment),
			SHA:            strings.TrimSpace(deployment.SHA),
			StatusID:       optionalString(statusID),
			Ref:            optionalString(strings.TrimSpace(deployment.Ref)),
			Task:           optionalString(strings.TrimSpace(deployment.Task)),
			State:          optionalString(strings.TrimSpace(deployment.State)),
			EnvironmentURL: optionalString(environmentURL),
			LogURL:         optionalString(logURL),
			RepositoryID:   nil,
		})
	}); err != nil {
		return collector.Fact{}, err
	}
	// Keyed on the provider's immutable ids plus scope/repository — never
	// on state or updated_at — so re-polling the same status re-derives
	// the identical key and upserts. scope_id+repository are load-bearing:
	// provider+deployment_id alone would collide across scopes sharing a
	// numeric id.
	stableKey := identity.StableID(KindDeploymentEvent, map[string]any{
		"provider":      string(provider),
		"scope_id":      ctx.claim.Scope.ID,
		"repository":    ctx.target.Repository,
		"deployment_id": deploymentID,
		"status_id":     statusID,
	})
	return newFact(ctx, KindDeploymentEvent, stableKey, deploymentID+":"+statusID, payload, redactions), nil
}
