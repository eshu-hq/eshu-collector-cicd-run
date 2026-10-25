// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"strings"

	"github.com/eshu-hq/eshu-sdk/collector"
	"github.com/eshu-hq/eshu-sdk/factschema/identity"
)

// appendTransportWarnings synthesizes one ci.warning fact per set
// transport-level partial flag (rate-limit cut, runs-window truncation,
// partial sub-pages) unless the caller already supplied that reason.
// Reason vocabulary mirrors recordPartialGeneration's reasons
// (runs_truncated, rate_limited) and the fixture normalizers'
// jobs:partial / artifacts:partial warnings.
func appendTransportWarnings(ctx emissionContext, provider Provider, evidence Evidence, facts []collector.Fact) []collector.Fact {
	synthesized := []WarningEvidence{}
	if evidence.RateLimited {
		synthesized = append(synthesized, WarningEvidence{
			Key:     "provider:rate_limited",
			Reason:  "rate_limited",
			Message: "provider rate limited; partial page committed",
		})
	}
	if evidence.Truncated {
		synthesized = append(synthesized, WarningEvidence{
			Key:     "runs:truncated",
			Reason:  "runs_truncated",
			Message: "run window truncated at max_runs ceiling",
		})
	}
	if evidence.JobsPartial {
		synthesized = append(synthesized, WarningEvidence{
			Key:     "jobs:partial",
			Reason:  "partial_jobs_payload",
			Message: "job metadata was partial or unavailable",
		})
	}
	if evidence.ArtifactsPartial {
		synthesized = append(synthesized, WarningEvidence{
			Key:     "artifacts:partial",
			Reason:  "partial_artifacts_payload",
			Message: "artifact metadata was partial or unavailable",
		})
	}
	present := map[string]bool{}
	for _, warning := range evidence.Warnings {
		present[strings.TrimSpace(warning.Reason)] = true
	}
	for _, warning := range synthesized {
		if present[warning.Reason] {
			continue
		}
		fact, err := warningFact(ctx, provider, evidence, warning)
		if err != nil {
			continue
		}
		facts = append(facts, fact)
	}
	return facts
}

func warningFact(ctx emissionContext, provider Provider, evidence Evidence, warning WarningEvidence) (collector.Fact, error) {
	reason := strings.TrimSpace(warning.Reason)
	payload := sharedPayload(ctx, provider, strings.TrimSpace(evidence.RunID), evidence.RunAttempt)
	payload["reason"] = reason
	payload["message"] = redactSensitiveText(strings.TrimSpace(warning.Message))
	payload["partial_generation"] = true
	stableKey := identity.StableID(KindWarning, map[string]any{
		"key":         strings.TrimSpace(warning.Key),
		"reason":      reason,
		"run_attempt": payload["run_attempt"],
		"run_id":      payload["run_id"],
	})
	return newFact(ctx, KindWarning, stableKey, strings.TrimSpace(warning.Key), payload, nil), nil
}
