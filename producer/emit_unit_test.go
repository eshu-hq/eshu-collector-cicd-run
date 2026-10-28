// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"errors"
	"testing"
	"time"

	"github.com/eshu-hq/eshu-sdk/collector"
)

func TestIsCancelledMatrix(t *testing.T) {
	t.Parallel()

	if IsCancelled(nil) {
		t.Error("IsCancelled(nil) = true, want false")
	}
	if IsCancelled(errors.New("boom")) {
		t.Error("IsCancelled(other) = true, want false")
	}
	if !IsCancelled(errCancelled) {
		t.Error("IsCancelled(sentinel) = false, want true")
	}
	if !IsCancelled(errors.Join(errors.New("outer"), errCancelled)) {
		t.Error("IsCancelled(wrapped sentinel) = false, want errors.Is matching")
	}
}

func TestBuildResultRequiresAuthorizer(t *testing.T) {
	t.Parallel()

	config := loadSingleTargetConfig(t)
	claim := windowClaim(config, windowScope())
	if _, err := BuildResult(claim, config, windowRunEvidence("1001"), nil); err == nil {
		t.Error("BuildResult(nil authorizer) = nil, want the fail-closed rejection")
	}
}

func TestBuildResultRejectsProviderMismatch(t *testing.T) {
	t.Parallel()

	config := loadSingleTargetConfig(t)
	claim := windowClaim(config, windowScope())
	evidence := windowRunEvidence("1001")
	evidence.Provider = string(ProviderGitLabCI)
	if _, err := BuildResult(claim, config, evidence, testAuthorizer{}); err == nil {
		t.Error("BuildResult(gitlab evidence on a github claim) = nil, want the provider rejection")
	}
}

func TestBuildFactsRequiresRunID(t *testing.T) {
	t.Parallel()

	config := loadSingleTargetConfig(t)
	claim := windowClaim(config, windowScope())
	target, err := NewRouter(config)
	if err != nil {
		t.Fatalf("NewRouter() error = %v, want nil", err)
	}
	resolved, err := target.Resolve(windowScope())
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil", err)
	}
	if _, err := buildFacts(claim, resolved, config.InstanceID, Evidence{}, time.Now().UTC()); err == nil {
		t.Error("buildFacts(blank run id) = nil, want rejection")
	}
}

func TestDefaultAttemptMatrix(t *testing.T) {
	t.Parallel()

	if got := defaultAttempt(""); got != "1" {
		t.Errorf("defaultAttempt(blank) = %q, want the first-attempt default", got)
	}
	if got := defaultAttempt(" 3 "); got != "3" {
		t.Errorf("defaultAttempt(padded) = %q, want the trimmed attempt", got)
	}
}

func TestNewFactFallsBackToDefaultSourceURI(t *testing.T) {
	t.Parallel()

	claim := validSDKClaim(windowScope(), "gen-fallback-1")
	target := ResolvedTarget{Provider: ProviderGitHubActions, Repository: "example-org/example-repo"}
	ctx := emissionContext{
		claim:             claim,
		target:            target,
		collectorInstance: "test-primary",
		observedAt:        time.Now().UTC(),
	}
	fact := newFact(ctx, KindRun, "key-1", "1001", map[string]any{}, nil)
	if fact.SourceRef.URI != "https://github.com/example-org/example-repo" {
		t.Errorf("SourceRef.URI = %q, want the provider default when no source URI is configured", fact.SourceRef.URI)
	}
}

func TestDefaultSourceURIMatrix(t *testing.T) {
	t.Parallel()

	gitlab := defaultSourceURI(ResolvedTarget{Provider: ProviderGitLabCI, Repository: "/group/project/"})
	if gitlab != "https://gitlab.com/group/project" {
		t.Errorf("defaultSourceURI(gitlab) = %q, want the trimmed project URL", gitlab)
	}
	github := defaultSourceURI(ResolvedTarget{Provider: ProviderGitHubActions, Repository: "example-org/example-repo"})
	if github != "https://github.com/example-org/example-repo" {
		t.Errorf("defaultSourceURI(github) = %q, want the repository URL", github)
	}
}

func TestMergeContractPayloadMatrix(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	if err := mergeContractPayload(map[string]any{}, func() (map[string]any, error) {
		return nil, boom
	}); !errors.Is(err, boom) {
		t.Errorf("mergeContractPayload(failing encode) = %v, want the encode error", err)
	}
	payload := map[string]any{"keep": "caller"}
	if err := mergeContractPayload(payload, func() (map[string]any, error) {
		return map[string]any{"keep": "encoded", "add": "new"}, nil
	}); err != nil {
		t.Fatalf("mergeContractPayload() error = %v, want nil", err)
	}
	if payload["keep"] != "caller" || payload["add"] != "new" {
		t.Errorf("mergeContractPayload() = %v, want caller fields kept and new fields added", payload)
	}
}

func TestDeduplicateFactsDropsRepeats(t *testing.T) {
	t.Parallel()

	mkFact := func(key string) collector.Fact {
		return collector.Fact{Kind: KindRun, StableKey: key}
	}
	got := deduplicateFacts([]collector.Fact{mkFact("a"), mkFact("a"), mkFact("b")})
	if len(got) != 2 || got[0].StableKey != "a" || got[1].StableKey != "b" {
		t.Errorf("deduplicateFacts() kept %d facts, want first-wins [a b]", len(got))
	}
}

func TestURLRedactionMatrix(t *testing.T) {
	t.Parallel()

	if got := urlRedaction("url", ""); got != nil {
		t.Errorf("urlRedaction(blank) = %v, want nil", got)
	}
	if got := urlRedaction("url", "https://github.com/example-org/example-repo"); got != nil {
		t.Errorf("urlRedaction(clean) = %v, want nil", got)
	}
	got := urlRedaction("url", "https://user:pass@example.com/hook?token=secret")
	if len(got) != 1 || got[0].Field != "url" {
		t.Errorf("urlRedaction(credentialed) = %v, want one entry naming the field", got)
	}
}
