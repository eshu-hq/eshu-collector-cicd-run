// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	collector "github.com/eshu-hq/eshu-collector-cicd-run"
	"github.com/eshu-hq/eshu-collector-cicd-run/producer"
	sdk "github.com/eshu-hq/eshu-sdk/collector"
)

func claimEnv() func(string) string {
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

func claimRequest(t *testing.T) string {
	t.Helper()

	claim := sdk.Claim{
		ComponentID: collector.ComponentID, InstanceID: "cicd-run-primary",
		CollectorKind: collector.CollectorKind, SourceSystem: collector.SourceSystem,
		Scope:        sdk.Scope{ID: "ci-cd:github-actions:example-org/example-repo", Kind: "ci_cd_run_scope"},
		SourceRunID:  "123456",
		GenerationID: "gen-stdio-1", WorkItemID: "work-1",
		FencingToken: "7", Attempt: 1,
		Deadline:     time.Now().UTC().Add(time.Hour),
		ConfigHandle: "component-config:cicd-run",
	}
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
	rawEvidence, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	var decodedEvidence any
	if err := json.Unmarshal(rawEvidence, &decodedEvidence); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(sdk.Request{
		ProtocolVersion: sdk.ProtocolVersionV1Alpha1,
		Claim:           claim,
		Contract:        collector.Contract(),
		Config:          map[string]any{collector.EvidenceConfigKey: decodedEvidence},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestRunClaimWritesOneValidResultToStdout proves the stdio contract end to
// end: one bounded request in, one SDK-valid result with emitted facts out.
func TestRunClaimWritesOneValidResultToStdout(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{nil, {"--sdk-stdio"}} {
		var stdout, stderr bytes.Buffer
		err := run(context.Background(),
			args,
			strings.NewReader(claimRequest(t)),
			&stdout, &stderr, claimEnv())
		if err != nil {
			t.Fatalf("run(%v) error = %v, want nil", args, err)
		}
		var result sdk.Result
		if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &result); err != nil {
			t.Fatalf("decode result: %v\n%s", err, stdout.String())
		}
		if result.State != sdk.ResultComplete {
			t.Errorf("run(%v) state = %q, want %q", args, result.State, sdk.ResultComplete)
		}
		if len(result.Facts) == 0 {
			t.Errorf("run(%v) facts = 0, want the run observation", args)
		}
		if result.Claim.GenerationID != "gen-stdio-1" {
			t.Errorf("run(%v) echoes generation %q, want the request claim", args, result.Claim.GenerationID)
		}
		if _, err := sdk.NewValidator(collector.Contract()).ValidateResult(result); err != nil {
			t.Errorf("ValidateResult(stdio result) error = %v, want nil", err)
		}
	}
}

func TestRunClaimRejectsEmptyStdin(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), nil, strings.NewReader(""), &stdout, &stderr, claimEnv()); err == nil {
		t.Error("run(empty stdin) = nil, want a decode failure")
	}
	if stdout.Len() != 0 {
		t.Errorf("a failed claim wrote %d stdout bytes, want none", stdout.Len())
	}
}

func TestRunClaimRejectsTrailingArgument(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"extra"},
		strings.NewReader(claimRequest(t)), &stdout, &stderr, claimEnv())
	if err == nil {
		t.Error("run(extra argument) = nil, want rejection")
	}
}
