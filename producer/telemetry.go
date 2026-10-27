// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

// Telemetry signal names the external producer must preserve verbatim
// (SHIM_MAP section 4). Instance IDs, repository names, target URLs,
// token values, and raw API payloads must never appear in metric labels.
//
// No-Observability-Change: the eshu_dp_ci_cd_run_* counters, the fetch
// duration histogram, and the two spans already cover the claimed path.
// Emission of these signals stays host-side (the host additionally
// records grant decisions per emission per #6709); this package only locks
// the names so a future exporter cannot drift from the in-tree contract.
const (
	// SpanObserve is emitted per claim observation.
	SpanObserve = "cicd_run.observe"
	// SpanFetch is emitted per provider fetch.
	SpanFetch = "cicd_run.fetch"
	// MetricFetchDuration is the fetch-duration histogram.
	MetricFetchDuration = "eshu_dp_ci_cd_run_fetch_duration_seconds"
	// MetricProviderRequests counts provider requests.
	MetricProviderRequests = "eshu_dp_ci_cd_run_provider_requests_total"
	// MetricFactsEmitted counts emitted facts.
	MetricFactsEmitted = "eshu_dp_ci_cd_run_facts_emitted_total"
	// MetricRateLimited counts rate-limited provider responses.
	MetricRateLimited = "eshu_dp_ci_cd_run_rate_limited_total"
	// MetricPartialGenerations counts partial generations by reason.
	MetricPartialGenerations = "eshu_dp_ci_cd_run_partial_generations_total"
)

// Metric label names. Provider values are "github_actions" |
// "gitlab_ci"; status_class and fact_kind carry only the bounded
// vocabularies in SHIM_MAP section 4.
const (
	// LabelProvider is the provider dimension.
	LabelProvider = "provider"
	// LabelStatusClass is the outcome-class dimension.
	LabelStatusClass = "status_class"
	// LabelFactKind is the fact-kind dimension.
	LabelFactKind = "fact_kind"
	// LabelReason is the partial-generation reason dimension.
	LabelReason = "reason"
)

// Partial-reason vocabulary for MetricPartialGenerations, mirroring
// recordPartialGeneration in the in-tree runtime.
const (
	// ReasonJobsTruncated marks truncated job pages.
	ReasonJobsTruncated = "jobs_truncated"
	// ReasonArtifactsTruncated marks truncated artifact pages.
	ReasonArtifactsTruncated = "artifacts_truncated"
	// ReasonProviderWarning marks provider-attached warnings.
	ReasonProviderWarning = "provider_warning"
	// ReasonRunsTruncated marks a full runs window.
	ReasonRunsTruncated = "runs_truncated"
	// ReasonRunsBackfillGap marks a #5429 cross-cycle gap.
	ReasonRunsBackfillGap = "runs_backfill_gap"
	// ReasonDeploymentsTruncated marks a full Deployments API window.
	ReasonDeploymentsTruncated = "deployments_truncated"
)

// Provider status-class vocabulary for MetricProviderRequests,
// mirroring classifyProviderStatus/classifyProviderStatusForMode in the
// in-tree runtime (#9146 App-auth wiring): direct-App credential/mint
// errors and provider-read 401/403/404 use auth_error; host-token file
// or mandatory authentication refusals use auth_denied; static-token
// errors retain error. See ClassifyProviderStatusCode.
const (
	// StatusClassRateLimited marks provider throttling.
	StatusClassRateLimited = "rate_limited"
	// StatusClassError marks provider errors.
	StatusClassError = "error"
	// StatusClassAuthError marks direct-App auth failures.
	StatusClassAuthError = "auth_error"
	// StatusClassAuthDenied marks host-token refusals.
	StatusClassAuthDenied = "auth_denied"
)
