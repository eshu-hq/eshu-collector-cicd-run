// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/eshu-hq/eshu-collector-cicd-run/producer"
	sdk "github.com/eshu-hq/eshu-sdk/collector"
)

// handlerEnv returns a credential-from-env-only environment with one
// enabled claim-capable ci_cd_run instance. apiBaseURL overrides the
// provider base so tests fetch from a fake server; token material stays
// test-only.
func handlerEnv(apiBaseURL string) func(string) string {
	instances := `[{"instance_id":"cicd-run-primary","collector_kind":"ci_cd_run",` +
		`"mode":"continuous","enabled":true,"claims_enabled":true,` +
		`"configuration":{"targets":[{"provider":"github_actions",` +
		`"scope_id":"ci-cd:github-actions:example-org/example-repo",` +
		`"repository":"example-org/example-repo","token_env":"GITHUB_TOKEN",` +
		`"allowed_repositories":["example-org/example-repo"],` +
		`"api_base_url":"` + apiBaseURL + `","max_runs":1,"max_jobs":25,"max_artifacts":25}]}}]`
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

// hostMintedEnv is the host_minted twin: no token_env, no token file —
// the per-claim token arrives on the request config, never the
// environment.
func hostMintedEnv(apiBaseURL string) func(string) string {
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

// handlerRequest builds the host request the way extensionhost does: the
// claim plus config carrying only host-owned per-claim material — never
// an evidence key. The satellite fetches the observation itself.
func handlerRequest(claim sdk.Claim, config map[string]any) sdk.Request {
	if config == nil {
		config = map[string]any{}
	}
	return sdk.Request{
		ProtocolVersion: sdk.ProtocolVersionV1Alpha1,
		Claim:           claim,
		Contract:        Contract(),
		Config:          config,
	}
}

func hostTokenConfig(token, scope string) map[string]any {
	return map[string]any{
		producer.HostMintedTokenKey: map[string]any{"token": token, "scope_id": scope},
	}
}

// handleGitHubServer serves one complete run window plus an empty
// deployments list so generations commit complete. It asserts the Bearer [REDACTED]
func handleGitHubServer(t *testing.T, wantToken string) *httptest.Server {
	t.Helper()

	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+wantToken {
			t.Errorf("Authorization = %q, want the claim Bearer", got)
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

// testHandler wires a Handler to an activation plus the fake server's
// trust: the injected HTTPClient is the seam production leaves nil.
func testHandler(getenv func(string) string, server *httptest.Server) Handler {
	handler := Handler{Getenv: getenv}
	if server != nil {
		handler.HTTPClient = server.Client()
	}
	return handler
}

func failureClass(t *testing.T, result sdk.Result) string {
	t.Helper()

	if len(result.Statuses) != 1 {
		t.Fatalf("Statuses = %+v, want exactly one", result.Statuses)
	}
	return result.Statuses[0].FailureClass
}

// The fetch port's core regression: a claim with NO evidence key fetches
// live and emits facts, instead of terminal-failing invalid_config the
// way the missing host fetch loop forced before (#9337).
func TestHandleFetchesWithoutEvidenceKey(t *testing.T) {
	t.Parallel()

	server := handleGitHubServer(t, "token-for-tests-only")
	t.Cleanup(server.Close)

	handler := testHandler(handlerEnv(server.URL), server)
	result, err := handler.Handle(context.Background(), handlerRequest(handlerClaim(), nil))
	if err != nil {
		t.Fatalf("Handle() error = %v, want nil", err)
	}
	if result.State != sdk.ResultComplete {
		t.Errorf("Handle() state = %q, want %q", result.State, sdk.ResultComplete)
	}
	if len(result.Facts) == 0 {
		t.Fatal("Handle() facts = 0, want the fetched run observation")
	}
	if result.Claim.GenerationID != "gen-handler-1" {
		t.Errorf("Handle() echoes claim generation %q, want the request claim", result.Claim.GenerationID)
	}
	if _, err := sdk.NewValidator(Contract()).ValidateResult(result); err != nil {
		t.Errorf("ValidateResult(handler result) error = %v, want nil", err)
	}
}

func TestHandleHostMintedSpendsRequestToken(t *testing.T) {
	t.Parallel()

	server := handleGitHubServer(t, "minted-token-for-tests")
	t.Cleanup(server.Close)

	handler := testHandler(hostMintedEnv(server.URL), server)
	config := hostTokenConfig("minted-token-for-tests", "ci-cd:github-actions:example-org/example-repo")
	result, err := handler.Handle(context.Background(), handlerRequest(handlerClaim(), config))
	if err != nil {
		t.Fatalf("Handle() error = %v, want nil", err)
	}
	if result.State != sdk.ResultComplete {
		t.Errorf("Handle() state = %q, want %q", result.State, sdk.ResultComplete)
	}
	if len(result.Facts) == 0 {
		t.Fatal("Handle() facts = 0, want the fetched run observation")
	}
}

func TestHandleHostTokenFailuresAreTerminalSubClasses(t *testing.T) {
	t.Parallel()

	server := handleGitHubServer(t, "minted-token-for-tests")
	t.Cleanup(server.Close)

	for _, test := range []struct {
		name   string
		config map[string]any
		want   string
	}{
		{"missing token", map[string]any{}, "invalid_config.missing_host_token"},
		{"empty token", hostTokenConfig("", "ci-cd:github-actions:example-org/example-repo"), "invalid_config.empty_host_token"},
		{"scope mismatch", hostTokenConfig("minted-token-for-tests", "ci-cd:github-actions:other/repo"), "invalid_config.host_token_scope_mismatch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			handler := testHandler(hostMintedEnv(server.URL), server)
			result, err := handler.Handle(context.Background(), handlerRequest(handlerClaim(), test.config))
			if err != nil {
				t.Fatalf("Handle() error = %v, want nil", err)
			}
			if result.State != sdk.ResultTerminal {
				t.Fatalf("Handle() state = %q, want terminal", result.State)
			}
			if got := failureClass(t, result); got != test.want {
				t.Errorf("FailureClass = %q, want %q", got, test.want)
			}
			if len(result.Facts) != 0 {
				t.Errorf("Handle() facts = %d, want 0: terminal never emits", len(result.Facts))
			}
		})
	}
}

func TestHandleProviderDeniedIsTerminalSubClass(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad credentials", http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)

	handler := testHandler(handlerEnv(server.URL), server)
	result, err := handler.Handle(context.Background(), handlerRequest(handlerClaim(), nil))
	if err != nil {
		t.Fatalf("Handle() error = %v, want nil", err)
	}
	if result.State != sdk.ResultTerminal {
		t.Fatalf("Handle() state = %q, want terminal", result.State)
	}
	if got := failureClass(t, result); got != "invalid_config.provider_denied" {
		t.Errorf("FailureClass = %q, want invalid_config.provider_denied", got)
	}
}

func TestHandleTransientIsRetryable(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream fault", http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)

	handler := testHandler(handlerEnv(server.URL), server)
	result, err := handler.Handle(context.Background(), handlerRequest(handlerClaim(), nil))
	if err != nil {
		t.Fatalf("Handle() error = %v, want nil", err)
	}
	if result.State != sdk.ResultRetryable {
		t.Fatalf("Handle() state = %q, want retryable", result.State)
	}
	if len(result.Facts) != 0 {
		t.Errorf("Handle() facts = %d, want 0: retryable never emits", len(result.Facts))
	}
}

func TestHandleEmptyWindowIsUnchanged(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/example-org/example-repo/actions/runs" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"total_count":0,"workflow_runs":[]}`))
	}))
	t.Cleanup(server.Close)

	handler := testHandler(handlerEnv(server.URL), server)
	result, err := handler.Handle(context.Background(), handlerRequest(handlerClaim(), nil))
	if err != nil {
		t.Fatalf("Handle() error = %v, want nil", err)
	}
	if result.State != sdk.ResultUnchanged {
		t.Errorf("Handle() state = %q, want unchanged for an empty window", result.State)
	}
}

func TestHandleBindingFailuresAreSubClasses(t *testing.T) {
	t.Parallel()

	server := handleGitHubServer(t, "token-for-tests-only")
	t.Cleanup(server.Close)

	handler := testHandler(handlerEnv(server.URL), server)
	foreign := handlerClaim()
	foreign.InstanceID = "cicd-run-elsewhere"
	result, err := handler.Handle(context.Background(), handlerRequest(foreign, nil))
	if err != nil {
		t.Fatalf("Handle() error = %v, want nil", err)
	}
	if result.State != sdk.ResultTerminal {
		t.Fatalf("Handle() state = %q, want terminal", result.State)
	}
	if got := failureClass(t, result); got != "invalid_config.claim_binding" {
		t.Errorf("FailureClass = %q, want invalid_config.claim_binding", got)
	}

	unknown := handlerClaim()
	unknown.Scope.ID = "ci-cd:github-actions:unknown/repo"
	result, err = handler.Handle(context.Background(), handlerRequest(unknown, nil))
	if err != nil {
		t.Fatalf("Handle() error = %v, want nil", err)
	}
	if result.State != sdk.ResultTerminal {
		t.Fatalf("Handle() state = %q, want terminal", result.State)
	}
	if got := failureClass(t, result); got != "invalid_config.target_scope_mismatch" {
		t.Errorf("FailureClass = %q, want invalid_config.target_scope_mismatch", got)
	}
}

func TestHandleForeignClaimWithCancelSubstringTerminalFails(t *testing.T) {
	t.Parallel()

	server := handleGitHubServer(t, "token-for-tests-only")
	t.Cleanup(server.Close)

	handler := testHandler(handlerEnv(server.URL), server)
	claim := handlerClaim()
	claim.InstanceID = "cicd-run-cancel-drill"
	result, err := handler.Handle(context.Background(), handlerRequest(claim, nil))
	if err != nil {
		t.Fatalf("Handle() error = %v, want nil: an InstanceID containing \"cancel\" must not misclassify a binding failure as a cancellation", err)
	}
	if result.State != sdk.ResultTerminal {
		t.Errorf("Handle() state = %q, want %q", result.State, sdk.ResultTerminal)
	}
	if len(result.Facts) != 0 {
		t.Errorf("Handle() facts = %d, want 0: a foreign claim never emits", len(result.Facts))
	}
}

func TestHandlerZeroValueGetenvFallsBackToProcessEnvironment(t *testing.T) {
	const marker = `[{"instance_id":"zero-value-probe"}]`
	t.Setenv(producer.EnvCollectorInstances, marker)

	var handler Handler
	if got := handler.getenv(producer.EnvCollectorInstances); got != marker {
		t.Errorf("Handler{}.getenv(%q) = %q, want the process environment value", producer.EnvCollectorInstances, got)
	}
}

// Direct github_app mints in-satellite: the App triple resolves from the
// environment, one installation token is minted per claim against the
// target base, and the fetch spends it — never accept-then-zero-facts.

// A stale evidence key on the request is ignored: fetch output is the
// only observation source, so an injected document can neither restore
// nor poison facts.
func TestHandleIgnoresStaleEvidenceKey(t *testing.T) {
	t.Parallel()

	server := handleGitHubServer(t, "token-for-tests-only")
	t.Cleanup(server.Close)

	handler := testHandler(handlerEnv(server.URL), server)
	config := map[string]any{"evidence": map[string]any{"provider": "github_actions", "run_id": "forged"}}
	result, err := handler.Handle(context.Background(), handlerRequest(handlerClaim(), config))
	if err != nil {
		t.Fatalf("Handle() error = %v, want nil", err)
	}
	if result.State != sdk.ResultComplete {
		t.Fatalf("Handle() state = %q, want %q: the stale key must not gate claims", result.State, sdk.ResultComplete)
	}
	found := false
	for _, fact := range result.Facts {
		if fact.Kind == producer.KindRun {
			if runID, _ := fact.Payload["run_id"].(string); runID == "123456" {
				found = true
			}
		}
	}
	if !found {
		t.Error("no ci.run fact for the fetched run 123456: the stale key must not poison facts")
	}
}
