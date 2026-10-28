// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"testing"
	"time"

	"github.com/eshu-hq/eshu-sdk/collector"
)

func TestBuildWindowResultRejectsForeignClaim(t *testing.T) {
	t.Parallel()

	config := loadSingleTargetConfig(t)
	claim := windowClaim(config, windowScope())
	claim.InstanceID = "foreign-instance"
	if _, err := BuildWindowResult(claim, config, Window{Provider: ProviderGitHubActions}, testAuthorizer{}); err == nil {
		t.Error("BuildWindowResult(foreign instance) = nil, want the claim-binding rejection")
	}
}

func TestBuildWindowResultTruncatedKeepsSuppliedWarning(t *testing.T) {
	t.Parallel()

	config := loadSingleTargetConfig(t)
	claim := windowClaim(config, windowScope())
	evidence := windowRunEvidence("1001")
	evidence.Warnings = []WarningEvidence{{
		Key:     "runs:truncated",
		Reason:  "runs_truncated",
		Message: "fetch already reported the truncation",
	}}
	result, err := BuildWindowResult(claim, config, Window{
		Provider:  ProviderGitHubActions,
		Evidences: []Evidence{evidence},
		Truncated: true,
	}, testAuthorizer{})
	if err != nil {
		t.Fatalf("BuildWindowResult() error = %v, want nil", err)
	}
	if result.State != collector.ResultPartial {
		t.Fatalf("State = %q, want partial for a truncated window", result.State)
	}
	seen := 0
	for _, fact := range result.Facts {
		if fact.Kind == KindWarning {
			seen++
		}
	}
	for _, status := range result.Statuses {
		if status.WarningCount != seen {
			t.Errorf("WarningCount = %d, want the %d emitted warning facts (no duplicate synthesis)", status.WarningCount, seen)
		}
	}
}

func TestAppendTransportWarningsDedupsSuppliedReasons(t *testing.T) {
	t.Parallel()

	claim := validSDKClaim(windowScope(), "gen-warn-1")
	target := ResolvedTarget{Provider: ProviderGitHubActions, Repository: "example-org/example-repo"}
	ctx := emissionContext{
		claim:             claim,
		target:            target,
		collectorInstance: "test-primary",
		observedAt:        time.Now().UTC(),
		sourceURI:         "https://github.com/example-org/example-repo",
	}
	evidence := windowRunEvidence("1001")
	evidence.RateLimited = true
	evidence.ArtifactsPartial = true
	evidence.Warnings = []WarningEvidence{
		{Key: "provider:rate_limited", Reason: "rate_limited", Message: "fetch already reported the throttle"},
	}
	facts := appendTransportWarnings(ctx, ProviderGitHubActions, evidence, nil)
	reasons := map[string]int{}
	for _, fact := range facts {
		if fact.Kind != KindWarning {
			t.Errorf("appendTransportWarnings() emitted kind %q, want only ci.warning facts", fact.Kind)
		}
		reason, _ := fact.Payload["reason"].(string)
		reasons[reason]++
	}
	// The supplied rate_limited reason suppresses the synthesized twin:
	// caller-supplied warnings ride the fact builders, never this seam.
	if reasons["rate_limited"] != 0 {
		t.Errorf("rate_limited warnings = %d, want zero (supplied reason suppresses synthesis)", reasons["rate_limited"])
	}
	if reasons["partial_artifacts_payload"] != 1 {
		t.Errorf("partial_artifacts_payload warnings = %d, want the synthesized artifact warning", reasons["partial_artifacts_payload"])
	}
}
