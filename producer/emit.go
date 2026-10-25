// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/eshu-hq/eshu-sdk/collector"
)

// errCancelled marks a cancelled claim: there is no result to return, so
// the caller must stop without committing anything.
var errCancelled = errors.New("claim cancelled: no result")

// RepositoryEvidence carries the provider-shaped repository locator the
// normalized fact payloads derive provenance from.

func BuildResult(claim collector.Claim, config Config, evidence Evidence, auth Authorizer) (collector.Result, error) {
	if auth == nil {
		return collector.Result{}, fmt.Errorf("authorizer is required: %w", errGrantDenied)
	}
	if err := auth.Authorize(claim); err != nil {
		return collector.Result{}, err
	}
	target, err := authorizeClaimBinding(claim, config)
	if err != nil {
		return collector.Result{}, err
	}
	if evidence.Cancelled {
		return collector.Result{}, errCancelled
	}
	now := time.Now().UTC()
	generation := collector.Generation{ID: claim.GenerationID, ObservedAt: now}
	if strings.TrimSpace(evidence.TerminalReason) != "" {
		return collector.Result{
			ProtocolVersion: collector.ProtocolVersionV1Alpha1,
			State:           collector.ResultTerminal,
			Claim:           claim,
			Generation:      generation,
			Statuses: []collector.Status{{
				Class:        collector.StatusFailure,
				FailureClass: "terminal_error",
			}},
		}, nil
	}
	if strings.TrimSpace(evidence.RetryableReason) != "" {
		return collector.Result{
			ProtocolVersion: collector.ProtocolVersionV1Alpha1,
			State:           collector.ResultRetryable,
			Claim:           claim,
			Generation:      generation,
			Statuses: []collector.Status{{
				Class:             collector.StatusFailure,
				FailureClass:      "transient_transport",
				RetryAfterSeconds: 30,
			}},
		}, nil
	}
	if evidence.Provider != "" && Provider(strings.TrimSpace(evidence.Provider)) != target.Provider {
		return collector.Result{}, fmt.Errorf("evidence provider %q does not match claim scope provider %q", evidence.Provider, target.Provider)
	}
	// An empty generation carries no evidence at all: complete the claim
	// without fact writes, before any run identity is required.
	if evidence.Empty {
		return collector.Result{
			ProtocolVersion: collector.ProtocolVersionV1Alpha1,
			State:           collector.ResultUnchanged,
			Claim:           claim,
			Generation:      generation,
		}, nil
	}
	facts, err := buildFacts(claim, target, config.InstanceID, evidence, now)
	if err != nil {
		return collector.Result{}, err
	}
	if len(facts) == 0 {
		return collector.Result{
			ProtocolVersion: collector.ProtocolVersionV1Alpha1,
			State:           collector.ResultUnchanged,
			Claim:           claim,
			Generation:      generation,
		}, nil
	}
	warningCount := countKind(facts, KindWarning)
	if isPartial(evidence) {
		return collector.Result{
			ProtocolVersion: collector.ProtocolVersionV1Alpha1,
			State:           collector.ResultPartial,
			Claim:           claim,
			Generation:      generation,
			Facts:           facts,
			Statuses: []collector.Status{{
				Class:        collector.StatusWarning,
				Partial:      true,
				WarningCount: warningCount,
				FactCount:    len(facts),
			}},
		}, nil
	}
	return collector.Result{
		ProtocolVersion: collector.ProtocolVersionV1Alpha1,
		State:           collector.ResultComplete,
		Claim:           claim,
		Generation:      generation,
		Facts:           facts,
		Statuses: []collector.Status{{
			Class:     collector.StatusComplete,
			FactCount: len(facts),
		}},
	}, nil
}

// isPartial reports whether the observed window was degraded. Explicit
// Evidence.Warnings are provider-attached degradation signals by
// construction (mirroring the in-tree provider_warning partial reason),
// so they mark the generation partial. Synthesized shape warnings that
// match the real API (for example GitLab's artifact digest warning, added
// inside buildGitLabFacts) do NOT mark partial: a whole generation can
// carry them and still complete, exactly as the in-tree GitLab path
// commits normally.

// isPartial reports whether the observed window was degraded. Explicit
// Evidence.Warnings are provider-attached degradation signals by
// construction (mirroring the in-tree provider_warning partial reason),
// so they mark the generation partial. Synthesized shape warnings that
// match the real API (for example GitLab's artifact digest warning, added
// inside buildGitLabFacts) do NOT mark partial: a whole generation can
// carry them and still complete, exactly as the in-tree GitLab path
// commits normally.
func isPartial(evidence Evidence) bool {
	return evidence.RateLimited || evidence.Truncated || evidence.JobsPartial || evidence.ArtifactsPartial || len(evidence.Warnings) > 0
}

func countKind(facts []collector.Fact, kind string) int {
	count := 0
	for _, fact := range facts {
		if fact.Kind == kind {
			count++
		}
	}
	return count
}

// emissionContext carries the claim-bound fields every fact shares.

// emissionContext carries the claim-bound fields every fact shares.
type emissionContext struct {
	claim             collector.Claim
	target            ResolvedTarget
	collectorInstance string
	observedAt        time.Time
	sourceURI         string
}

func buildFacts(claim collector.Claim, target ResolvedTarget, instanceID string, evidence Evidence, observedAt time.Time) ([]collector.Fact, error) {
	if strings.TrimSpace(evidence.RunID) == "" {
		return nil, fmt.Errorf("evidence run id is required")
	}
	provider := target.Provider
	ctx := emissionContext{
		claim:             claim,
		target:            target,
		collectorInstance: instanceID,
		observedAt:        observedAt,
		sourceURI:         target.SourceURI,
	}
	var facts []collector.Fact
	if provider == ProviderGitHubActions {
		githubFacts, err := buildGitHubFacts(ctx, provider, evidence)
		if err != nil {
			return nil, err
		}
		facts = append(facts, githubFacts...)
	} else {
		gitlabFacts, err := buildGitLabFacts(ctx, provider, evidence)
		if err != nil {
			return nil, err
		}
		facts = append(facts, gitlabFacts...)
	}
	facts = appendTransportWarnings(ctx, provider, evidence, facts)
	return deduplicateFacts(facts), nil
}

// appendTransportWarnings synthesizes one ci.warning fact per set
// transport-level partial flag (rate-limit cut, runs-window truncation,
// partial sub-pages) unless the caller already supplied that reason.
// Reason vocabulary mirrors recordPartialGeneration's reasons
// (runs_truncated, rate_limited) and the fixture normalizers'
// jobs:partial / artifacts:partial warnings.

// sharedPayload mirrors the in-tree sharedPayload/gitlabSharedPayload:
// the collector boundary fields every fact in one claim shares.
func sharedPayload(ctx emissionContext, provider Provider, runID, attempt string) map[string]any {
	return map[string]any{
		"collector_instance_id": ctx.collectorInstance,
		"provider":              string(provider),
		"run_id":                runID,
		"run_attempt":           defaultAttempt(attempt),
	}
}

func defaultAttempt(attempt string) string {
	if strings.TrimSpace(attempt) == "" {
		return "1"
	}
	return strings.TrimSpace(attempt)
}

// newFact maps one normalized payload to the SDK Fact shape per SHIM_MAP
// section 2: stable_key from the fixture stable key, source_ref bound to
// the claim scope and committed generation, payload from the
// factschema.EncodeCICD* contract payload, redactions from the URL/text
// sanitizers.

// newFact maps one normalized payload to the SDK Fact shape per SHIM_MAP
// section 2: stable_key from the fixture stable key, source_ref bound to
// the claim scope and committed generation, payload from the
// factschema.EncodeCICD* contract payload, redactions from the URL/text
// sanitizers.
func newFact(ctx emissionContext, kind, stableKey, recordID string, payload map[string]any, redactions []collector.Redaction) collector.Fact {
	uri := stripSensitiveURL(ctx.sourceURI)
	if uri == "" {
		uri = stripSensitiveURL(defaultSourceURI(ctx.target))
	}
	return collector.Fact{
		Kind:             kind,
		SchemaVersion:    SchemaVersion,
		StableKey:        stableKey,
		SourceConfidence: collector.SourceConfidenceReported,
		ObservedAt:       ctx.observedAt,
		SourceRef: collector.SourceRef{
			SourceSystem: ctx.claim.SourceSystem,
			ScopeID:      ctx.claim.Scope.ID,
			GenerationID: ctx.claim.GenerationID,
			FactKey:      stableKey,
			URI:          uri,
			RecordID:     recordID,
		},
		Payload:    payload,
		Redactions: redactions,
	}
}

func defaultSourceURI(target ResolvedTarget) string {
	if target.Provider == ProviderGitLabCI {
		return "https://gitlab.com/" + strings.Trim(target.Repository, "/")
	}
	return "https://github.com/" + strings.Trim(target.Repository, "/")
}

// mergeContractPayload merges a typed factschema encoding into the raw
// payload without overwriting caller-set fields. It mirrors the in-tree
// encode_helpers merge.

// mergeContractPayload merges a typed factschema encoding into the raw
// payload without overwriting caller-set fields. It mirrors the in-tree
// encode_helpers merge.
func mergeContractPayload(payload map[string]any, encode func() (map[string]any, error)) error {
	encoded, err := encode()
	if err != nil {
		return err
	}
	for key, value := range encoded {
		if _, exists := payload[key]; !exists {
			payload[key] = value
		}
	}
	return nil
}

func stringPtr(value string) *string {
	return &value
}

// optionalString returns nil for a blank value, matching the DeploymentEvent
// contract's *string "omitempty" optional fields.

// optionalString returns nil for a blank value, matching the DeploymentEvent
// contract's *string "omitempty" optional fields.
func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return stringPtr(value)
}

func boolPtr(value bool) *bool {
	return &value
}

func int64Ptr(value int64) *int64 {
	return &value
}

func nonEmptyStrings(values ...string) []string {
	var out []string
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func deduplicateFacts(facts []collector.Fact) []collector.Fact {
	seen := map[string]bool{}
	out := make([]collector.Fact, 0, len(facts))
	for _, fact := range facts {
		key := fact.Kind + "\x00" + fact.StableKey
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, fact)
	}
	return out
}

// urlRedaction records a redaction entry when sanitization removed
// material from a URL field. The SDK requires non-blank field and reason.
func urlRedaction(field, raw string) []collector.Redaction {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	stripped := stripSensitiveURL(raw)
	if stripped == strings.TrimSpace(raw) {
		return nil
	}
	return []collector.Redaction{{
		Field:  field,
		Reason: "credential_or_query_stripped",
	}}
}
