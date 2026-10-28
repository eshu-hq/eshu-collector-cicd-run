// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	collector "github.com/eshu-hq/eshu-collector-cicd-run"
	"github.com/eshu-hq/eshu-collector-cicd-run/producer"
	sdk "github.com/eshu-hq/eshu-sdk/collector"
)

// stdioEnv is the host_minted activation the stdio proof serves: no
// token_env, no token file — the per-claim token arrives on the request
// config exactly as extensionhost injects it.
func stdioEnv(apiBaseURL string) func(string) string {
	instances := `[{"instance_id":"cicd-run-primary","collector_kind":"ci_cd_run",` +
		`"mode":"continuous","enabled":true,"claims_enabled":true,` +
		`"configuration":{"targets":[{"provider":"github_actions",` +
		`"scope_id":"ci-cd:github-actions:example-org/example-repo",` +
		`"repository":"example-org/example-repo","auth_mode":"host_minted",` +
		`"allowed_repositories":["example-org/example-repo"],` +
		`"api_base_url":"` + apiBaseURL + `","max_runs":1,"max_jobs":25,"max_artifacts":25}]}}]`
	return func(key string) string {
		switch key {
		case producer.EnvCollectorInstances:
			return instances
		case producer.EnvCollectorInstanceID:
			return "cicd-run-primary"
		default:
			return ""
		}
	}
}

// stdioRequest builds the host request exactly as extensionhost builds
// it: the claim plus a config carrying only the host-minted token object
// {token, scope_id} — never an evidence key. The satellite fetches the
// observation itself.
func stdioRequest(t *testing.T) string {
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
	raw, err := json.Marshal(sdk.Request{
		ProtocolVersion: sdk.ProtocolVersionV1Alpha1,
		Claim:           claim,
		Contract:        collector.Contract(),
		Config: map[string]any{
			producer.HostMintedTokenKey: map[string]any{
				"token":    "stdio-minted-token",
				"scope_id": "ci-cd:github-actions:example-org/example-repo",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// The stdio proof serves plain HTTP: the binary under test leaves the
// production transport nil (no injection seam on runClaim), so the fake
// provider must chain to the default trust. TLS coverage lives in the
// producer and handler suites, which inject the test server's client.
func stdioServer(t *testing.T) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer stdio-minted-token" {
			t.Errorf("Authorization = %q, want the host-minted Bearer", got)
		}
		switch r.URL.Path {
		case "/repos/example-org/example-repo/actions/runs":
			_, _ = w.Write([]byte(`{"total_count":1,"workflow_runs":[{` +
				`"id":123456,"workflow_id":42,"name":"CI","run_attempt":1,"run_number":42,` +
				`"event":"push","status":"completed","conclusion":"success",` +
				`"head_branch":"main","head_sha":"0123456789abcdef0123456789abcdef01234567",` +
				`"run_started_at":"2026-10-27T00:00:00Z","updated_at":"2026-10-27T00:05:00Z",` +
				`"html_url":"https://github.com/example-org/example-repo/actions/runs/123456",` +
				`"path":".github/workflows/ci.yml",` +
				`"repository":{"full_name":"example-org/example-repo","html_url":"https://github.com/example-org/example-repo"},` +
				`"actor":{"login":"octocat"}}]}`))
		case "/repos/example-org/example-repo/actions/runs/123456/jobs":
			_, _ = w.Write([]byte(`{"total_count":1,"jobs":[{` +
				`"id":2001,"name":"build","status":"completed","conclusion":"success",` +
				`"started_at":"2026-10-27T00:01:00Z","completed_at":"2026-10-27T00:04:00Z",` +
				`"labels":["ubuntu-latest"],"steps":[]}]}`))
		case "/repos/example-org/example-repo/actions/runs/123456/artifacts":
			_, _ = w.Write([]byte(`{"total_count":0,"artifacts":[]}`))
		case "/repos/example-org/example-repo/deployments":
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}

// TestRunClaimWritesOneValidResultToStdout proves the host contract end
// to end: one bounded host-shaped request in (claim plus host-minted
// token object, no evidence key), one SDK-valid result with fetched
// facts out.
func TestRunClaimWritesOneValidResultToStdout(t *testing.T) {
	t.Parallel()

	server := stdioServer(t)
	t.Cleanup(server.Close)

	for _, args := range [][]string{nil, {"--sdk-stdio"}} {
		var stdout, stderr bytes.Buffer
		err := run(context.Background(),
			args,
			strings.NewReader(stdioRequest(t)),
			&stdout, &stderr, stdioEnv(server.URL))
		if err != nil {
			t.Fatalf("run(%v) error = %v, want nil", args, err)
		}
		var result sdk.Result
		if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &result); err != nil {
			t.Fatalf("decode result: %v\n%s", err, stdout.String())
		}
		if result.State != sdk.ResultComplete {
			t.Errorf("run(%v) state = %q, want %q (stderr: %s)", args, result.State, sdk.ResultComplete, stderr.String())
		}
		if len(result.Facts) == 0 {
			t.Errorf("run(%v) facts = 0, want the fetched run observation", args)
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
	if err := run(context.Background(), nil, strings.NewReader(""), &stdout, &stderr, stdioEnv("https://api.github.com")); err == nil {
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
		strings.NewReader(stdioRequest(t)), &stdout, &stderr, stdioEnv("https://api.github.com"))
	if err == nil {
		t.Error("run(extra argument) = nil, want rejection")
	}
}
