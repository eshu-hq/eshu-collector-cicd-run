// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/eshu-hq/eshu-collector-cicd-run/producer"
	sdk "github.com/eshu-hq/eshu-sdk/collector"
)

// handlerEnv returns a credential-from-env-only environment with one
// enabled claim-capable ci_cd_run instance and one github_actions target.
func handlerEnv() func(string) string {
	instances := `[{"instance_id":"cicd-run-primary","collector_kind":"ci_cd_run",` +
		`"mode":"continuous","enabled":true,"claims_enabled":true,` +
		`"configuration":{"targets":[{"provider":"github_actions",` +
		`"scope_id":"ci-cd:github-actions:example-org/example-repo",` +
		`"repository":"example-org/example-repo","token_env":"GITHUB_TOKEN",` +
		`"allowed_repositories":["example-org/example-repo"],` +
		`"api_base_url":"https://api.github.com","max_runs":1,"max_jobs":25,"max_artifacts":25}]}}]`
	return func(key string) string {
		switch key {
		case producer.EnvCollectorInstances:
			return instances
		case producer.EnvCollectorInstanceID:
			return "cicd-run-primary"
		case "GITHUB_TOKEN":
			return "token-for-tests-only"
		default:
			return ""
		}
	}
}

func handlerClaim() sdk.Claim {
	return sdk.Claim{
		ComponentID: ComponentID, InstanceID: "cicd-run-primary",
		CollectorKind: CollectorKind, SourceSystem: SourceSystem,
		Scope:        sdk.Scope{ID: "ci-cd:github-actions:example-org/example-repo", Kind: "ci_cd_run_scope"},
		SourceRunID:  "123456",
		GenerationID: "gen-handler-1", WorkItemID: "work-1",
		FencingToken: "7", Attempt: 1,
		Deadline:     time.Now().UTC().Add(time.Hour),
		ConfigHandle: "component-config:cicd-run",
	}
}

func handlerRequest(t *testing.T, claim sdk.Claim, evidence any) sdk.Request {
	t.Helper()

	config := map[string]any{}
	if evidence != nil {
		raw, err := json.Marshal(evidence)
		if err != nil {
			t.Fatal(err)
		}
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		config[EvidenceConfigKey] = decoded
	}
	return sdk.Request{
		ProtocolVersion: sdk.ProtocolVersionV1Alpha1,
		Claim:           claim,
		Contract:        Contract(),
		Config:          config,
	}
}

func emptyEvidence() producer.Evidence {
	return producer.Evidence{Provider: "github_actions", Empty: true}
}

func TestHandleEmptyEvidenceCompletesUnchanged(t *testing.T) {
	t.Parallel()

	handler := Handler{Getenv: handlerEnv()}
	result, err := handler.Handle(context.Background(), handlerRequest(t, handlerClaim(), emptyEvidence()))
	if err != nil {
		t.Fatalf("Handle() error = %v, want nil", err)
	}
	if result.State != sdk.ResultUnchanged {
		t.Errorf("Handle() state = %q, want %q", result.State, sdk.ResultUnchanged)
	}
	if len(result.Facts) != 0 {
		t.Errorf("Handle() facts = %d, want 0", len(result.Facts))
	}
	if result.Claim.GenerationID != "gen-handler-1" {
		t.Errorf("Handle() echoes claim generation %q, want the request claim", result.Claim.GenerationID)
	}
}

func TestHandleTerminalEvidenceTerminalFails(t *testing.T) {
	t.Parallel()

	handler := Handler{Getenv: handlerEnv()}
	evidence := producer.Evidence{Provider: "github_actions", TerminalReason: "auth denied at claim scope"}
	result, err := handler.Handle(context.Background(), handlerRequest(t, handlerClaim(), evidence))
	if err != nil {
		t.Fatalf("Handle() error = %v, want nil", err)
	}
	if result.State != sdk.ResultTerminal {
		t.Errorf("Handle() state = %q, want %q", result.State, sdk.ResultTerminal)
	}
}

func TestHandleCancelledEvidenceReturnsNoResult(t *testing.T) {
	t.Parallel()

	handler := Handler{Getenv: handlerEnv()}
	evidence := producer.Evidence{Provider: "github_actions", Cancelled: true}
	if _, err := handler.Handle(context.Background(), handlerRequest(t, handlerClaim(), evidence)); err == nil {
		t.Error("Handle(cancelled) = nil, want the cancellation error (no result)")
	}
}

func TestHandleMissingEvidenceTerminalFailsWithoutQuoting(t *testing.T) {
	t.Parallel()

	handler := Handler{Getenv: handlerEnv()}
	result, err := handler.Handle(context.Background(), handlerRequest(t, handlerClaim(), nil))
	if err != nil {
		t.Fatalf("Handle() error = %v, want nil", err)
	}
	if result.State != sdk.ResultTerminal {
		t.Errorf("Handle() state = %q, want %q", result.State, sdk.ResultTerminal)
	}
}

func TestHandleForeignClaimTerminalFails(t *testing.T) {
	t.Parallel()

	handler := Handler{Getenv: handlerEnv()}
	claim := handlerClaim()
	claim.InstanceID = "cicd-run-elsewhere"
	result, err := handler.Handle(context.Background(), handlerRequest(t, claim, emptyEvidence()))
	if err != nil {
		t.Fatalf("Handle() error = %v, want nil", err)
	}
	if result.State != sdk.ResultTerminal {
		t.Errorf("Handle() state = %q, want %q", result.State, sdk.ResultTerminal)
	}
	if len(result.Facts) != 0 {
		t.Errorf("Handle() facts = %d, want 0: a foreign claim never emits", len(result.Facts))
	}
}

func TestHandleCompleteEvidenceEmitsValidatedFacts(t *testing.T) {
	t.Parallel()

	handler := Handler{Getenv: handlerEnv()}
	evidence := producer.Evidence{
		Provider:   "github_actions",
		RunID:      "123456",
		RunAttempt: "1",
		Repository: producer.RepositoryEvidence{
			FullName: "example-org/example-repo",
			HTMLURL:  "https://github.com/example-org/example-repo",
		},
		RunName: "CI", RunNumber: "42", Event: "push",
		Status: "completed", Conclusion: "success", Branch: "main",
		CommitSHA: "0123456789abcdef0123456789abcdef01234567",
		RunURL:    "https://github.com/example-org/example-repo/actions/runs/123456",
		StartedAt: "2026-10-27T00:00:00Z", UpdatedAt: "2026-10-27T00:05:00Z",
		Actor: "octocat",
	}
	result, err := handler.Handle(context.Background(), handlerRequest(t, handlerClaim(), evidence))
	if err != nil {
		t.Fatalf("Handle() error = %v, want nil", err)
	}
	if result.State != sdk.ResultComplete {
		t.Errorf("Handle() state = %q, want %q", result.State, sdk.ResultComplete)
	}
	if len(result.Facts) == 0 {
		t.Fatal("Handle() facts = 0, want the run observation")
	}
	validator := sdk.NewValidator(Contract())
	if _, err := validator.ValidateResult(result); err != nil {
		t.Errorf("ValidateResult(handler result) error = %v, want nil", err)
	}
}
