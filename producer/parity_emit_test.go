// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"strings"
	"testing"

	"github.com/eshu-hq/eshu-sdk/collector"
)

func TestParityEmissionKindsKeysFencing(t *testing.T) {
	t.Parallel()

	fixture := loadParityFixture(t)
	instancesJSON := fixtureInstancesJSON(t, fixture)
	config, err := LoadConfig(instancesJSON, "cicd-run-primary", fixtureGetenv(fixture, instancesJSON))
	if err != nil {
		t.Fatalf("LoadConfig(fixture) error = %v, want nil", err)
	}
	router, err := NewRouter(config)
	if err != nil {
		t.Fatalf("NewRouter() error = %v, want nil", err)
	}
	claim := testClaim(router, config, "ci-cd:github-actions:example-org/example-repo", "gen-8854-a")
	result, err := BuildResult(claim, config, fullGitHubEvidence(), testAuthorizer{})
	if err != nil {
		t.Fatalf("BuildResult(full github evidence) error = %v, want nil", err)
	}
	if result.State != collector.ResultComplete {
		t.Errorf("State = %q, want %q", result.State, collector.ResultComplete)
	}
	// A whole generation carries every evidence kind but ci.warning.
	assertEmittedKinds(t, result, withoutKind(fixture.Expected.EmittedKinds, "ci.warning"))
	assertGenerationFencing(t, result, claim)

	// A provider-attached degradation warning keeps every kind and flips
	// the generation to partial.
	degradedEvidence := fullGitHubEvidence()
	degradedEvidence.Warnings = []WarningEvidence{{Key: "fixture-warning:0", Reason: "provider_warning_note", Message: "provider attached a warning"}}
	degraded, err := BuildResult(claim, config, degradedEvidence, testAuthorizer{})
	if err != nil {
		t.Fatalf("BuildResult(degraded evidence) error = %v, want nil", err)
	}
	if degraded.State != collector.ResultPartial {
		t.Errorf("degraded State = %q, want %q", degraded.State, collector.ResultPartial)
	}
	assertEmittedKinds(t, degraded, fixture.Expected.EmittedKinds)

	// Stable keys are generation-agnostic: the same evidence under a new
	// generation re-derives the identical key set (idempotent upsert at
	// projection), and a different run derives a different key set.
	retry := testClaim(router, config, "ci-cd:github-actions:example-org/example-repo", "gen-8854-b")
	retryResult, err := BuildResult(retry, config, fullGitHubEvidence(), testAuthorizer{})
	if err != nil {
		t.Fatalf("BuildResult(retry generation) error = %v, want nil", err)
	}
	if !equalKeySets(result.Facts, retryResult.Facts) {
		t.Error("stable keys differ across generations for identical evidence; keys must be generation-agnostic")
	}
	other := fullGitHubEvidence()
	other.RunID = "999999"
	otherResult, err := BuildResult(retry, config, other, testAuthorizer{})
	if err != nil {
		t.Fatalf("BuildResult(other run) error = %v, want nil", err)
	}
	if equalKeySets(result.Facts, otherResult.Facts) {
		t.Error("stable keys identical across different runs; keys must separate runs")
	}
	for _, fact := range result.Facts {
		if fact.SchemaVersion != fixture.Expected.Schema {
			t.Errorf("fact %q schema = %q, want %q", fact.Kind, fact.SchemaVersion, fixture.Expected.Schema)
		}
		if fact.SourceConfidence != collector.SourceConfidenceReported {
			t.Errorf("fact %q confidence = %q, want reported", fact.Kind, fact.SourceConfidence)
		}
		if len(fact.StableKey) != 64 {
			t.Errorf("fact %q stable key %q is not a 64-hex digest", fact.Kind, fact.StableKey)
		}
	}

	// Every emitted result validates against the exact pinned SDK pair.
	validator := collector.NewValidator(Contract())
	if _, err := validator.ValidateResult(result); err != nil {
		t.Errorf("ValidateResult(complete) error = %v, want SDK-accepted result", err)
	}
}

func TestParityGitLabScopeNarrowerThanGitHub(t *testing.T) {
	t.Parallel()

	fixture := loadParityFixture(t)
	instancesJSON := fixtureInstancesJSON(t, fixture)
	config, err := LoadConfig(instancesJSON, "cicd-run-primary", fixtureGetenv(fixture, instancesJSON))
	if err != nil {
		t.Fatalf("LoadConfig(fixture) error = %v, want nil", err)
	}
	router, err := NewRouter(config)
	if err != nil {
		t.Fatalf("NewRouter() error = %v, want nil", err)
	}
	claim := testClaim(router, config, "gitlab-ci://gitlab.com/eshu-hq/demo", "gen-8854-gl")
	result, err := BuildResult(claim, config, gitLabEvidence(), testAuthorizer{})
	if err != nil {
		t.Fatalf("BuildResult(gitlab evidence) error = %v, want nil", err)
	}
	if result.State != collector.ResultComplete {
		t.Errorf("gitlab State = %q, want %q (digest warnings match the real API shape; they do not mark partial)", result.State, collector.ResultComplete)
	}
	for _, fact := range result.Facts {
		switch fact.Kind {
		case "ci.pipeline_definition", "ci.step", "ci.trigger_edge", "ci.environment_observation", "ci.deployment_event":
			t.Errorf("gitlab evidence emitted %q, out of v1 scope for gitlab_ci", fact.Kind)
		}
	}
	assertHasKinds(t, result, "ci.run", "ci.job", "ci.artifact", "ci.warning")
	validator := collector.NewValidator(Contract())
	if _, err := validator.ValidateResult(result); err != nil {
		t.Errorf("ValidateResult(gitlab) error = %v, want SDK-accepted result", err)
	}
}

func TestParityFailureSignals(t *testing.T) {
	t.Parallel()

	fixture := loadParityFixture(t)
	instancesJSON := fixtureInstancesJSON(t, fixture)
	config, err := LoadConfig(instancesJSON, "cicd-run-primary", fixtureGetenv(fixture, instancesJSON))
	if err != nil {
		t.Fatalf("LoadConfig(fixture) error = %v, want nil", err)
	}
	router, err := NewRouter(config)
	if err != nil {
		t.Fatalf("NewRouter() error = %v, want nil", err)
	}
	claim := testClaim(router, config, "ci-cd:github-actions:example-org/example-repo", "gen-8854-f")

	// Empty generation: nothing to write, complete the claim without facts.
	empty, err := BuildResult(claim, config, Evidence{Provider: "github_actions", Empty: true}, testAuthorizer{})
	if err != nil {
		t.Fatalf("BuildResult(empty) error = %v, want nil", err)
	}
	if empty.State != collector.ResultUnchanged || len(empty.Facts) != 0 {
		t.Errorf("empty = (%q, %d facts), want (unchanged, 0 facts)", empty.State, len(empty.Facts))
	}

	// Partial page (rate-limited, truncated window): partial + warning
	// status + ci.warning facts carrying the reachable evidence.
	partialEvidence := fullGitHubEvidence()
	partialEvidence.RateLimited = true
	partialEvidence.Truncated = true
	partial, err := BuildResult(claim, config, partialEvidence, testAuthorizer{})
	if err != nil {
		t.Fatalf("BuildResult(partial) error = %v, want nil", err)
	}
	if partial.State != collector.ResultPartial {
		t.Errorf("partial State = %q, want partial", partial.State)
	}
	assertHasKinds(t, partial, "ci.warning")
	hasWarningStatus := false
	for _, status := range partial.Statuses {
		if status.Class == collector.StatusWarning || status.Partial {
			hasWarningStatus = true
		}
	}
	if !hasWarningStatus {
		t.Error("partial result carries no warning/partial status")
	}
	validator := collector.NewValidator(Contract())
	if _, err := validator.ValidateResult(partial); err != nil {
		t.Errorf("ValidateResult(partial) error = %v, want SDK-accepted result", err)
	}

	// Transient transport failure: release the claim for a bounded retry.
	retryable, err := BuildResult(claim, config, Evidence{Provider: "github_actions", RetryableReason: "transport reset"}, testAuthorizer{})
	if err != nil {
		t.Fatalf("BuildResult(retryable) error = %v, want nil", err)
	}
	if retryable.State != collector.ResultRetryable || len(retryable.Facts) != 0 {
		t.Errorf("retryable = (%q, %d facts), want (retryable, 0 facts)", retryable.State, len(retryable.Facts))
	}
	if _, err := validator.ValidateResult(retryable); err != nil {
		t.Errorf("ValidateResult(retryable) error = %v, want SDK-accepted result", err)
	}

	// Terminal failure (auth denied at claim scope): terminal-fail, no facts.
	terminal, err := BuildResult(claim, config, Evidence{Provider: "github_actions", TerminalReason: "auth denied at claim scope"}, testAuthorizer{})
	if err != nil {
		t.Fatalf("BuildResult(terminal) error = %v, want nil", err)
	}
	if terminal.State != collector.ResultTerminal || len(terminal.Facts) != 0 {
		t.Errorf("terminal = (%q, %d facts), want (terminal, 0 facts)", terminal.State, len(terminal.Facts))
	}
	if _, err := validator.ValidateResult(terminal); err != nil {
		t.Errorf("ValidateResult(terminal) error = %v, want SDK-accepted result", err)
	}

	// Cancellation: no result at all.
	if _, err := BuildResult(claim, config, Evidence{Provider: "github_actions", Cancelled: true}, testAuthorizer{}); err == nil {
		t.Error("BuildResult(cancelled) = nil, want cancellation error (no result)")
	}

	// A foreign claim (wrong instance, blank generation, blank fencing)
	// never emits: the producer treats unclaimed scope as foreign.
	foreign := claim
	foreign.InstanceID = "cicd-run-elsewhere"
	if _, err := BuildResult(foreign, config, fullGitHubEvidence(), testAuthorizer{}); err == nil {
		t.Error("BuildResult(foreign instance) = nil, want fail-closed rejection")
	}
	blankFencing := claim
	blankFencing.FencingToken = ""
	if _, err := BuildResult(blankFencing, config, fullGitHubEvidence(), testAuthorizer{}); err == nil {
		t.Error("BuildResult(blank fencing token) = nil, want fail-closed rejection")
	}
	blankGeneration := claim
	blankGeneration.GenerationID = ""
	if _, err := BuildResult(blankGeneration, config, fullGitHubEvidence(), testAuthorizer{}); err == nil {
		t.Error("BuildResult(blank generation) = nil, want fail-closed rejection")
	}
}

func TestParityGrantEnforcementFailClosed(t *testing.T) {
	t.Parallel()

	fixture := loadSingleTargetConfig(t)
	claim := testClaimForScope(t, fixture, "ci-cd:github-actions:example-org/example-repo", "gen-8854-g")
	if err := (DenyAllAuthorizer{}).Authorize(claim); err == nil {
		t.Fatal("DenyAllAuthorizer.Authorize() = nil, want fail-closed denial")
	}
	if _, err := BuildResult(claim, fixture, fullGitHubEvidence(), DenyAllAuthorizer{}); err == nil {
		t.Fatal("BuildResult(deny-all grant) = nil, want fail-closed denial with no result")
	} else if !strings.Contains(err.Error(), "grant") {
		t.Fatalf("BuildResult() error = %q, want a grant-denial error", err.Error())
	}
}

func TestParityCredentialedURIFailsSDKValidation(t *testing.T) {
	t.Parallel()

	// The pinned SDK is the fail-closed backstop: a fact whose source_ref
	// URI carries credentials never validates, so a sanitizer miss cannot
	// become persisted provenance.
	validator := collector.NewValidator(Contract())
	claim := validSDKClaim("ci-cd:github-actions:example-org/example-repo", "gen-8854-u")
	result, err := BuildResult(claim, singleTargetConfig(t), fullGitHubEvidence(), testAuthorizer{})
	if err != nil {
		t.Fatalf("BuildResult() error = %v, want nil", err)
	}
	bad := result
	bad.Facts = append([]collector.Fact{}, result.Facts...)
	bad.Facts[0].SourceRef.URI = "https://svc:SECRET@example.com/org/repo"
	if _, err := validator.ValidateResult(bad); err == nil {
		t.Error("ValidateResult(credentialed uri) = nil, want SDK fail-closed rejection")
	}
}

// testAuthorizer is the test-only stand-in for a core-issued grant: the
// production binary wires DenyAllAuthorizer until #6709 issuance lands.

func (testAuthorizer) Authorize(claim collector.Claim) error {
	if strings.TrimSpace(claim.FencingToken) == "" {
		return errMissingFencing
	}
	return nil
}

func fullGitHubEvidence() Evidence {
	return Evidence{
		Provider:   "github_actions",
		RunID:      "123456",
		RunAttempt: "1",
		Repository: RepositoryEvidence{
			FullName: "example-org/example-repo",
			HTMLURL:  "https://github.com/example-org/example-repo",
		},
		RunName:    "CI",
		Event:      "push",
		Status:     "completed",
		Conclusion: "success",
		Branch:     "main",
		CommitSHA:  "abc123def456",
		RunURL:     "https://github.com/example-org/example-repo/actions/runs/123456",
		Actor:      "octocat",
		Workflow:   &WorkflowEvidence{ID: "789", Name: "CI", Path: ".github/workflows/ci.yml", State: "active", Trigger: "push"},
		Jobs: []JobEvidence{{
			ID:          "111",
			Name:        "build",
			Status:      "completed",
			Conclusion:  "success",
			Environment: "prod",
			Steps:       []StepEvidence{{Number: "1", Name: "Run actions/checkout@v4", Status: "completed", Conclusion: "success"}},
		}},
		Artifacts: []ArtifactEvidence{{ID: "222", Name: "dist", SizeBytes: 128}},
		Triggers:  []TriggerEvidence{{Kind: "workflow_call", SourceProvider: "github_actions", SourceRunID: "123455"}},
		Deployments: []DeploymentEvidence{{
			ID: "333", StatusID: "444", Environment: "prod", SHA: "abc123def456",
			EnvironmentURL: "https://example.com/deploy",
		}},
	}
}

func gitLabEvidence() Evidence {
	return Evidence{
		Provider:   "gitlab_ci",
		RunID:      "42",
		RunAttempt: "1",
		Repository: RepositoryEvidence{
			WebURL: "https://gitlab.com/eshu-hq/demo/-/pipelines/42",
		},
		RunName: "test",
		Status:  "success",
		Jobs: []JobEvidence{{
			ID:     "7",
			Name:   "test",
			Status: "success",
			InlineArtifacts: []ArtifactEvidence{
				{ID: "7:report.xml", Name: "report.xml", SizeBytes: 64},
			},
		}},
	}
}

func assertEmittedKinds(t *testing.T, result collector.Result, want []string) {
	t.Helper()

	var got []string
	for _, fact := range result.Facts {
		got = append(got, fact.Kind)
	}
	if !equalAsSets(got, want) {
		t.Errorf("emitted kinds = %v, want exactly %v", got, want)
	}
}

func assertHasKinds(t *testing.T, result collector.Result, want ...string) {
	t.Helper()

	present := map[string]bool{}
	for _, fact := range result.Facts {
		present[fact.Kind] = true
	}
	for _, kind := range want {
		if !present[kind] {
			t.Errorf("result missing kind %q (have %v)", kind, result.Facts)
		}
	}
}

func assertGenerationFencing(t *testing.T, result collector.Result, claim collector.Claim) {
	t.Helper()

	if result.Generation.ID != claim.GenerationID {
		t.Errorf("generation.id = %q, want claim generation %q", result.Generation.ID, claim.GenerationID)
	}
	if result.Claim.FencingToken != claim.FencingToken {
		t.Errorf("result fencing token = %q, want claim fencing %q", result.Claim.FencingToken, claim.FencingToken)
	}
	for _, fact := range result.Facts {
		if fact.SourceRef.ScopeID != claim.Scope.ID {
			t.Errorf("fact %q scope = %q, want claim scope %q", fact.Kind, fact.SourceRef.ScopeID, claim.Scope.ID)
		}
		if fact.SourceRef.GenerationID != claim.GenerationID {
			t.Errorf("fact %q generation = %q, want %q", fact.Kind, fact.SourceRef.GenerationID, claim.GenerationID)
		}
		if fact.SourceRef.FactKey != fact.StableKey {
			t.Errorf("fact %q fact_key != stable_key", fact.Kind)
		}
	}
}

func equalKeySets(left, right []collector.Fact) bool {
	if len(left) != len(right) {
		return false
	}
	counts := map[string]int{}
	for _, fact := range left {
		counts[fact.Kind+"\x00"+fact.StableKey]++
	}
	for _, fact := range right {
		key := fact.Kind + "\x00" + fact.StableKey
		counts[key]--
		if counts[key] < 0 {
			return false
		}
	}
	return true
}

func withoutKind(kinds []string, drop string) []string {
	out := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		if kind != drop {
			out = append(out, kind)
		}
	}
	return out
}
