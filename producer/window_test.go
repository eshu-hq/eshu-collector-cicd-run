// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"testing"
	"time"

	"github.com/eshu-hq/eshu-sdk/collector"
)

func windowRunEvidence(runID string) Evidence {
	return Evidence{
		Provider:   string(ProviderGitHubActions),
		RunID:      runID,
		RunAttempt: "1",
		Repository: RepositoryEvidence{
			FullName: "example-org/example-repo",
			HTMLURL:  "https://github.com/example-org/example-repo",
		},
		RunName: "CI", RunNumber: "42", Event: "push",
		Status: "completed", Conclusion: "success", Branch: "main",
		CommitSHA: "0123456789abcdef0123456789abcdef01234567",
		RunURL:    "https://github.com/example-org/example-repo/actions/runs/" + runID,
		StartedAt: "2026-10-27T00:00:00Z", UpdatedAt: "2026-10-27T00:05:00Z",
		Actor: "octocat",
	}
}

func windowClaim(config Config, scopeID string) collector.Claim {
	return collector.Claim{
		ComponentID:   "dev.eshu.collector.cicd-run",
		InstanceID:    config.InstanceID,
		CollectorKind: CollectorKind,
		SourceSystem:  "ci_cd_run",
		Scope:         collector.Scope{ID: scopeID, Kind: "ci_cd_run"},
		SourceRunID:   "window-claim",
		GenerationID:  "gen-window-1",
		WorkItemID:    "work-window-1",
		FencingToken:  "fencing-window",
		Attempt:       1,
		Deadline:      time.Now().UTC().Add(time.Hour),
		ConfigHandle:  "component-config:cicd-run",
	}
}

func windowScope() string { return "ci-cd:github-actions:example-org/example-repo" }

// BuildWindowResult composes one fetched window into the claimed result:
// every run's facts merge into a single generation (idempotent upsert at
// projection, same as the in-tree multi-run generation), window truncation
// or throttling flips the generation partial with its warning, and an
// empty window completes nothing (unchanged, no facts).
func TestBuildWindowResultMergesRuns(t *testing.T) {
	t.Parallel()

	config := loadSingleTargetConfig(t)
	claim := windowClaim(config, windowScope())
	result, err := BuildWindowResult(claim, config, Window{
		Provider:  ProviderGitHubActions,
		Evidences: []Evidence{windowRunEvidence("1001"), windowRunEvidence("1002")},
	}, testAuthorizer{})
	if err != nil {
		t.Fatalf("BuildWindowResult() error = %v, want nil", err)
	}
	if result.State != collector.ResultComplete {
		t.Errorf("State = %q, want complete", result.State)
	}
	runs := map[string]bool{}
	for _, fact := range result.Facts {
		if fact.Kind == KindRun {
			runs[fact.SourceRef.RecordID] = true
		}
	}
	if !runs["1001"] || !runs["1002"] {
		t.Errorf("run facts = %v, want both window runs in one generation", runs)
	}
	if result.Claim.GenerationID != "gen-window-1" {
		t.Errorf("echoes generation %q, want the request claim", result.Claim.GenerationID)
	}
	if _, err := collector.NewValidator(Contract()).ValidateResult(result); err != nil {
		t.Errorf("ValidateResult(window) error = %v, want nil", err)
	}
}

func TestBuildWindowResultEmptyIsUnchanged(t *testing.T) {
	t.Parallel()

	config := loadSingleTargetConfig(t)
	claim := windowClaim(config, windowScope())
	result, err := BuildWindowResult(claim, config, Window{Provider: ProviderGitHubActions}, testAuthorizer{})
	if err != nil {
		t.Fatalf("BuildWindowResult() error = %v, want nil", err)
	}
	if result.State != collector.ResultUnchanged {
		t.Errorf("State = %q, want unchanged for an empty window", result.State)
	}
	if len(result.Facts) != 0 {
		t.Errorf("Facts = %d, want 0", len(result.Facts))
	}
}

func TestBuildWindowResultTruncatedIsPartial(t *testing.T) {
	t.Parallel()

	config := loadSingleTargetConfig(t)
	claim := windowClaim(config, windowScope())
	result, err := BuildWindowResult(claim, config, Window{
		Provider:  ProviderGitHubActions,
		Evidences: []Evidence{windowRunEvidence("1001")},
		Truncated: true,
	}, testAuthorizer{})
	if err != nil {
		t.Fatalf("BuildWindowResult() error = %v, want nil", err)
	}
	if result.State != collector.ResultPartial {
		t.Fatalf("State = %q, want partial for a truncated window", result.State)
	}
	found := false
	for _, fact := range result.Facts {
		if fact.Kind == KindWarning {
			if reason, _ := fact.Payload["reason"].(string); reason == "runs_truncated" {
				found = true
			}
		}
	}
	if !found {
		t.Error("partial window carries no runs_truncated ci.warning fact")
	}
}

func TestBuildWindowResultRateLimitedIsPartial(t *testing.T) {
	t.Parallel()

	config := loadSingleTargetConfig(t)
	claim := windowClaim(config, windowScope())
	result, err := BuildWindowResult(claim, config, Window{
		Provider:    ProviderGitHubActions,
		Evidences:   []Evidence{windowRunEvidence("1001")},
		RateLimited: true,
	}, testAuthorizer{})
	if err != nil {
		t.Fatalf("BuildWindowResult() error = %v, want nil", err)
	}
	if result.State != collector.ResultPartial {
		t.Fatalf("State = %q, want partial for a throttled window", result.State)
	}
	found := false
	for _, fact := range result.Facts {
		if fact.Kind == KindWarning {
			if reason, _ := fact.Payload["reason"].(string); reason == "rate_limited" {
				found = true
			}
		}
	}
	if !found {
		t.Error("partial window carries no rate_limited ci.warning fact")
	}
}

func TestBuildWindowResultRunPartialIsPartial(t *testing.T) {
	t.Parallel()

	config := loadSingleTargetConfig(t)
	claim := windowClaim(config, windowScope())
	partial := windowRunEvidence("1001")
	partial.JobsPartial = true
	result, err := BuildWindowResult(claim, config, Window{
		Provider:  ProviderGitHubActions,
		Evidences: []Evidence{partial, windowRunEvidence("1002")},
	}, testAuthorizer{})
	if err != nil {
		t.Fatalf("BuildWindowResult() error = %v, want nil", err)
	}
	if result.State != collector.ResultPartial {
		t.Errorf("State = %q, want partial when one run is partial", result.State)
	}
}

func TestBuildWindowResultTerminalEvidenceIsTerminal(t *testing.T) {
	t.Parallel()

	config := loadSingleTargetConfig(t)
	claim := windowClaim(config, windowScope())
	terminal := windowRunEvidence("1001")
	terminal.TerminalReason = "auth denied at claim scope"
	result, err := BuildWindowResult(claim, config, Window{
		Provider:  ProviderGitHubActions,
		Evidences: []Evidence{terminal},
	}, testAuthorizer{})
	if err != nil {
		t.Fatalf("BuildWindowResult() error = %v, want nil", err)
	}
	if result.State != collector.ResultTerminal {
		t.Errorf("State = %q, want terminal", result.State)
	}
	if len(result.Facts) != 0 {
		t.Errorf("Facts = %d, want 0: a terminal window never emits", len(result.Facts))
	}
}

func TestBuildWindowResultRetryableEvidenceIsRetryable(t *testing.T) {
	t.Parallel()

	config := loadSingleTargetConfig(t)
	claim := windowClaim(config, windowScope())
	retryable := windowRunEvidence("1001")
	retryable.RetryableReason = "transport reset"
	result, err := BuildWindowResult(claim, config, Window{
		Provider:  ProviderGitHubActions,
		Evidences: []Evidence{retryable},
	}, testAuthorizer{})
	if err != nil {
		t.Fatalf("BuildWindowResult() error = %v, want nil", err)
	}
	if result.State != collector.ResultRetryable {
		t.Errorf("State = %q, want retryable", result.State)
	}
	if len(result.Facts) != 0 {
		t.Errorf("Facts = %d, want 0: a retryable window never emits", len(result.Facts))
	}
}

func TestBuildWindowResultSkipsEmptyEvidence(t *testing.T) {
	t.Parallel()

	config := loadSingleTargetConfig(t)
	claim := windowClaim(config, windowScope())
	result, err := BuildWindowResult(claim, config, Window{
		Provider:  ProviderGitHubActions,
		Evidences: []Evidence{{Provider: string(ProviderGitHubActions), Empty: true}, windowRunEvidence("1001")},
	}, testAuthorizer{})
	if err != nil {
		t.Fatalf("BuildWindowResult() error = %v, want nil", err)
	}
	if result.State != collector.ResultComplete {
		t.Errorf("State = %q, want complete: empty entries contribute nothing", result.State)
	}
}

func TestBuildWindowResultRejectsNilAuthorizer(t *testing.T) {
	t.Parallel()

	config := loadSingleTargetConfig(t)
	claim := windowClaim(config, windowScope())
	if _, err := BuildWindowResult(claim, config, Window{
		Evidences: []Evidence{windowRunEvidence("1001")},
	}, nil); err == nil {
		t.Error("BuildWindowResult(nil authorizer) = nil, want the grant denial")
	}
}

func TestBuildWindowResultRejectsProviderMismatch(t *testing.T) {
	t.Parallel()

	config := loadSingleTargetConfig(t)
	claim := windowClaim(config, windowScope())
	mismatched := windowRunEvidence("1001")
	mismatched.Provider = string(ProviderGitLabCI)
	if _, err := BuildWindowResult(claim, config, Window{
		Provider:  ProviderGitHubActions,
		Evidences: []Evidence{mismatched},
	}, testAuthorizer{}); err == nil {
		t.Error("BuildWindowResult(provider mismatch) = nil, want the binding failure")
	}
}

func TestBuildWindowResultDeniesAuthorizer(t *testing.T) {
	t.Parallel()

	config := loadSingleTargetConfig(t)
	claim := windowClaim(config, windowScope())
	if _, err := BuildWindowResult(claim, config, Window{
		Evidences: []Evidence{windowRunEvidence("1001")},
	}, DenyAllAuthorizer{}); err == nil {
		t.Error("BuildWindowResult(deny-all) = nil, want the grant denial")
	}
}
