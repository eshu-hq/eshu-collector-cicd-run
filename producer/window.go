// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"fmt"
	"strings"
	"time"

	"github.com/eshu-hq/eshu-sdk/collector"
)

// Window is one fetched provider window for a claim: one normalized
// Evidence per run or pipeline in provider order, plus the window-level
// degradation signals the composer turns into partial-generation warnings.
// It ports the in-tree RunPage/PipelinePage window shape onto the
// Evidence seam: the emission builders consume each entry unchanged.
type Window struct {
	// Provider is the fetching provider and must match the bound target.
	Provider Provider
	// Evidences holds one entry per fetched run or pipeline, newest first
	// as the provider returns them.
	Evidences []Evidence
	// Truncated marks the max_runs window ceiling: the provider reports
	// more runs beyond the fetched window.
	Truncated bool
	// RateLimited marks provider throttling observed mid-window with a
	// partial prefix already committed.
	RateLimited bool
}

// BuildWindowResult maps one authorized claim plus its fetched window to
// the SDK Result the host commits, following the SHIM_MAP outcome table
// across the whole window:
//
//	envelopes committed                          → complete
//	rate-limited window, partial prefix committed → partial + warning
//	truncation cap cut                            → partial + warning
//	transient transport failure                  → retryable, no facts
//	terminal failure                             → terminal, no facts
//	empty window                                 → unchanged, no facts
//
// A terminal entry beats a retryable one: a deterministic refusal is never
// hidden behind a transient. Duplicate delivery stays safe: stable keys
// are deterministic per run and committed generations are idempotent at
// the core boundary.
func BuildWindowResult(claim collector.Claim, config Config, window Window, auth Authorizer) (collector.Result, error) {
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
	live := make([]Evidence, 0, len(window.Evidences))
	for _, evidence := range window.Evidences {
		if evidence.Empty || strings.TrimSpace(evidence.RunID) == "" {
			continue
		}
		if evidence.Provider != "" && Provider(strings.TrimSpace(evidence.Provider)) != target.Provider {
			return collector.Result{}, fmt.Errorf("evidence provider %q does not match claim scope provider %q", evidence.Provider, target.Provider)
		}
		live = append(live, evidence)
	}
	now := time.Now().UTC()
	generation := collector.Generation{ID: claim.GenerationID, ObservedAt: now}
	for _, evidence := range live {
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
	}
	for _, evidence := range live {
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
	}
	if len(live) == 0 {
		return collector.Result{
			ProtocolVersion: collector.ProtocolVersionV1Alpha1,
			State:           collector.ResultUnchanged,
			Claim:           claim,
			Generation:      generation,
		}, nil
	}
	ctx := emissionContext{
		claim:             claim,
		target:            target,
		collectorInstance: config.InstanceID,
		observedAt:        now,
		sourceURI:         target.SourceURI,
	}
	facts := make([]collector.Fact, 0, len(live))
	partial := window.Truncated || window.RateLimited
	for _, evidence := range live {
		runFacts, err := buildFacts(claim, target, config.InstanceID, evidence, now)
		if err != nil {
			return collector.Result{}, err
		}
		facts = append(facts, runFacts...)
		if isPartial(evidence) {
			partial = true
		}
	}
	facts = appendWindowWarnings(ctx, target.Provider, live[0], facts, window.Truncated, window.RateLimited)
	facts = deduplicateFacts(facts)
	if len(facts) == 0 {
		return collector.Result{
			ProtocolVersion: collector.ProtocolVersionV1Alpha1,
			State:           collector.ResultUnchanged,
			Claim:           claim,
			Generation:      generation,
		}, nil
	}
	if partial {
		return collector.Result{
			ProtocolVersion: collector.ProtocolVersionV1Alpha1,
			State:           collector.ResultPartial,
			Claim:           claim,
			Generation:      generation,
			Facts:           facts,
			Statuses: []collector.Status{{
				Class:        collector.StatusWarning,
				Partial:      true,
				WarningCount: countKind(facts, KindWarning),
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

// appendWindowWarnings synthesizes the window-level transport warnings
// (runs-window truncation, mid-window throttling) anchored on the first
// committed run, unless the fetch already supplied that reason. Reason
// vocabulary mirrors recordPartialGeneration's reasons (runs_truncated,
// rate_limited).
func appendWindowWarnings(ctx emissionContext, provider Provider, anchor Evidence, facts []collector.Fact, truncated, rateLimited bool) []collector.Fact {
	synthesized := []WarningEvidence{}
	if rateLimited {
		synthesized = append(synthesized, WarningEvidence{
			Key:     "provider:rate_limited",
			Reason:  "rate_limited",
			Message: "provider rate limited; partial window committed",
		})
	}
	if truncated {
		synthesized = append(synthesized, WarningEvidence{
			Key:     "runs:truncated",
			Reason:  "runs_truncated",
			Message: "run window truncated at max_runs ceiling",
		})
	}
	present := map[string]bool{}
	for _, warning := range anchor.Warnings {
		present[strings.TrimSpace(warning.Reason)] = true
	}
	for _, warning := range synthesized {
		if present[warning.Reason] {
			continue
		}
		fact, err := warningFact(ctx, provider, anchor, warning)
		if err != nil {
			continue
		}
		facts = append(facts, fact)
	}
	return facts
}
