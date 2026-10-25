// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

// RepositoryEvidence carries the provider-shaped repository locator the
// normalized fact payloads derive provenance from.
type RepositoryEvidence struct {
	// FullName is the provider repository locator ("owner/repo" for
	// GitHub Actions).
	FullName string
	// HTMLURL is the provider repository URL (GitHub Actions).
	HTMLURL string
	// WebURL is the provider pipeline URL (GitLab CI).
	WebURL string
}

// WorkflowEvidence describes one provider workflow definition (GitHub
// Actions only: GitLab's Pipelines API exposes no separate workflow
// resource, so gitlab_ci never emits ci.pipeline_definition).

// WorkflowEvidence describes one provider workflow definition (GitHub
// Actions only: GitLab's Pipelines API exposes no separate workflow
// resource, so gitlab_ci never emits ci.pipeline_definition).

// WorkflowEvidence describes one provider workflow definition (GitHub
// Actions only: GitLab's Pipelines API exposes no separate workflow
// resource, so gitlab_ci never emits ci.pipeline_definition).
type WorkflowEvidence struct {
	ID      string
	Name    string
	Path    string
	State   string
	Trigger string
}

// StepEvidence describes one provider step under a job (GitHub Actions
// only: the GitLab Jobs API reports no step-level breakdown).

// StepEvidence describes one provider step under a job (GitHub Actions
// only: the GitLab Jobs API reports no step-level breakdown).

// StepEvidence describes one provider step under a job (GitHub Actions
// only: the GitLab Jobs API reports no step-level breakdown).
type StepEvidence struct {
	Number     string
	Name       string
	Status     string
	Conclusion string
	StartedAt  string
	Completed  string
}

// JobEvidence describes one provider job under a run.

// JobEvidence describes one provider job under a run.

// JobEvidence describes one provider job under a run.
type JobEvidence struct {
	ID          string
	Name        string
	Status      string
	Conclusion  string
	StartedAt   string
	CompletedAt string
	Labels      []string
	// Environment gates a ci.environment_observation fact (GitHub only).
	Environment string
	// DeploymentStatus rides the environment observation (GitHub only).
	DeploymentStatus string
	// Steps emit ci.step facts (GitHub only).
	Steps []StepEvidence
	// InlineArtifacts emit ci.artifact facts per job (GitLab: artifacts
	// report inline with no separate paginated endpoint).
	InlineArtifacts []ArtifactEvidence
	// Stage is GitLab's pipeline stage, carried as a payload field only.
	Stage string
}

// ArtifactEvidence describes one provider artifact.

// ArtifactEvidence describes one provider artifact.

// ArtifactEvidence describes one provider artifact.
type ArtifactEvidence struct {
	ID          string
	Name        string
	Type        string
	Digest      string
	SizeBytes   int64
	DownloadURL string
	CreatedAt   string
	ExpiresAt   string
	Expired     bool
}

// TriggerEvidence describes one explicit run trigger edge (GitHub only).

// TriggerEvidence describes one explicit run trigger edge (GitHub only).

// TriggerEvidence describes one explicit run trigger edge (GitHub only).
type TriggerEvidence struct {
	Kind           string
	SourceProvider string
	SourceRunID    string
}

// DeploymentEvidence describes one deployment/status pair (GitHub
// Deployments API, v1 scope).

// DeploymentEvidence describes one deployment/status pair (GitHub
// Deployments API, v1 scope).

// DeploymentEvidence describes one deployment/status pair (GitHub
// Deployments API, v1 scope).
type DeploymentEvidence struct {
	ID             string
	StatusID       string
	Environment    string
	SHA            string
	Ref            string
	Task           string
	State          string
	EnvironmentURL string
	LogURL         string
	// OriginalEnvironment is the parent deployment's environment before a
	// redeploy retargeted it; the status value wins when present.
	OriginalEnvironment string
	// Description is the deployment (status-less) or status description.
	Description string
	// CreatedAt/UpdatedAt are the status timestamps when present,
	// otherwise the parent deployment's.
	CreatedAt string
	UpdatedAt string
	// ProductionEnvironment/TransientEnvironment are the parent
	// deployment's environment-classification flags.
	ProductionEnvironment bool
	TransientEnvironment  bool
}

// WarningEvidence describes one non-fatal collection warning. Warnings
// always ride a partial generation (partial_generation: true).

// WarningEvidence describes one non-fatal collection warning. Warnings
// always ride a partial generation (partial_generation: true).

// WarningEvidence describes one non-fatal collection warning. Warnings
// always ride a partial generation (partial_generation: true).
type WarningEvidence struct {
	Key     string
	Reason  string
	Message string
}

// Evidence is the normalized provider observation for one claim: the
// emission seam the parity fixture covers. It mirrors the in-tree
// RunSnapshot/PipelineSnapshot split (one evidence value per provider),
// minus the live HTTP fetch, which stays a follow-up lane: this bounded
// port proves kinds, keys, fencing, and failure signals from normalized
// evidence, and the fetch layer reuses these builders unchanged.

// Evidence is the normalized provider observation for one claim: the
// emission seam the parity fixture covers. It mirrors the in-tree
// RunSnapshot/PipelineSnapshot split (one evidence value per provider),
// minus the live HTTP fetch, which stays a follow-up lane: this bounded
// port proves kinds, keys, fencing, and failure signals from normalized
// evidence, and the fetch layer reuses these builders unchanged.

// Evidence is the normalized provider observation for one claim: the
// emission seam the parity fixture covers. It mirrors the in-tree
// RunSnapshot/PipelineSnapshot split (one evidence value per provider),
// minus the live HTTP fetch, which stays a follow-up lane: this bounded
// port proves kinds, keys, fencing, and failure signals from normalized
// evidence, and the fetch layer reuses these builders unchanged.
type Evidence struct {
	// Provider is "github_actions" or "gitlab_ci" and must match the
	// claim scope's registered provider.
	Provider string
	// RunID is the provider run identifier (pipeline ID for GitLab).
	RunID string
	// RunAttempt defaults to "1" when blank.
	RunAttempt string
	Repository RepositoryEvidence
	// Run display fields.
	RunName   string
	RunNumber string
	Event     string
	// Source is the GitLab pipeline source (carried as "event").
	Source     string
	Status     string
	Conclusion string
	Branch     string
	CommitSHA  string
	RunURL     string
	StartedAt  string
	UpdatedAt  string
	Actor      string
	// Workflow gates a ci.pipeline_definition fact (GitHub only).
	Workflow *WorkflowEvidence
	Jobs     []JobEvidence
	// Artifacts are top-level paginated artifacts (GitHub only; GitLab
	// artifacts ride JobEvidence.InlineArtifacts).
	Artifacts []ArtifactEvidence
	// Triggers and Deployments are GitHub-only in v1 scope.
	Triggers    []TriggerEvidence
	Deployments []DeploymentEvidence
	Warnings    []WarningEvidence
	// JobsPartial / ArtifactsPartial mark truncated sub-pages; Truncated
	// marks the max_runs window ceiling; RateLimited marks provider
	// throttling with a partial page committed.
	JobsPartial      bool
	ArtifactsPartial bool
	Truncated        bool
	RateLimited      bool
	// Empty marks an empty generation (ok=false): nothing to write.
	Empty bool
	// Cancelled marks claim cancellation: no result at all.
	Cancelled bool
	// RetryableReason marks transient failure: release for bounded retry.
	RetryableReason string
	// TerminalReason marks terminal failure (for example auth denied at
	// claim scope, or a contract violation): terminal-fail the claim.
	TerminalReason string
}

// BuildResult maps one authorized claim plus its normalized evidence to
// the SDK Result the host commits, following the SHIM_MAP outcome table:
//
//	envelopes committed                          → complete
//	rate-limited page, partial page committed    → partial + warning + ci.warning
//	truncation cap cut                           → partial + warning + truncated ci.warning
//	transient transport failure                  → retryable, no facts
//	terminal failure                             → terminal, no facts
//	empty generation                             → unchanged, no facts
//	cancellation                                 → no result (error)
//
// Duplicate delivery stays safe: stable keys are deterministic and
// committed generations are idempotent at the core boundary.

// BuildResult maps one authorized claim plus its normalized evidence to
// the SDK Result the host commits, following the SHIM_MAP outcome table:
//
//	envelopes committed                          → complete
//	rate-limited page, partial page committed    → partial + warning + ci.warning
//	truncation cap cut                           → partial + warning + truncated ci.warning
//	transient transport failure                  → retryable, no facts
//	terminal failure                             → terminal, no facts
//	empty generation                             → unchanged, no facts
//	cancellation                                 → no result (error)
//
// Duplicate delivery stays safe: stable keys are deterministic and
// committed generations are idempotent at the core boundary.
