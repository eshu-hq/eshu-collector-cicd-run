// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

// App-credential claim tests: the direct github_app mint, its
// distinct denial, and the unexported outcome helpers.

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/eshu-hq/eshu-collector-cicd-run/producer"
	sdk "github.com/eshu-hq/eshu-sdk/collector"
)

func TestHandleGitHubAppMintsInSatellite(t *testing.T) {
	t.Parallel()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v, want nil", err)
	}
	privatePEM := string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}))
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/installations/456/access_tokens":
			if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
				t.Errorf("mint Authorization = %q, want a Bearer JWT", got)
			}
			_, _ = w.Write([]byte(`{"token":"freshly-minted-token"}`))
		case "/repos/example-org/example-repo/actions/runs":
			if got := r.Header.Get("Authorization"); got != "Bearer freshly-minted-token" {
				t.Errorf("fetch Authorization = %q, want the minted token", got)
			}
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
			_, _ = w.Write([]byte(`{"total_count":0,"jobs":[]}`))
		case "/repos/example-org/example-repo/actions/runs/123456/artifacts":
			_, _ = w.Write([]byte(`{"total_count":0,"artifacts":[]}`))
		case "/repos/example-org/example-repo/deployments":
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	instances := `[{"instance_id":"cicd-run-primary","collector_kind":"ci_cd_run",` +
		`"mode":"continuous","enabled":true,"claims_enabled":true,` +
		`"configuration":{"targets":[{"provider":"github_actions",` +
		`"scope_id":"ci-cd:github-actions:example-org/example-repo",` +
		`"repository":"example-org/example-repo","auth_mode":"github_app",` +
		`"github_app_id_env":"APP_ID","github_app_installation_id_env":"INSTALL_ID",` +
		`"github_app_private_key_env":"APP_KEY",` +
		`"allowed_repositories":["example-org/example-repo"],` +
		`"api_base_url":"` + server.URL + `","max_runs":1,"max_jobs":25,"max_artifacts":25}]}}]`
	getenv := func(key string) string {
		switch key {
		case producer.EnvCollectorInstances:
			return instances
		case producer.EnvCollectorInstanceID:
			return "cicd-run-primary"
		case "APP_ID":
			return "123"
		case "INSTALL_ID":
			return "456"
		case "APP_KEY":
			return privatePEM
		default:
			return ""
		}
	}
	handler := testHandler(getenv, server)
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
}

// A denied mint fails closed with its distinct class — never an
// accepted claim with zero facts.
func TestHandleGitHubAppDeniedMintIsDistinctTerminal(t *testing.T) {
	t.Parallel()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v, want nil", err)
	}
	privatePEM := string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}))
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/app/installations/456/access_tokens" {
			http.Error(w, "bad credentials", http.StatusUnauthorized)
			return
		}
		t.Errorf("unexpected path %s: no provider read may follow a denied mint", r.URL.Path)
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)

	instances := `[{"instance_id":"cicd-run-primary","collector_kind":"ci_cd_run",` +
		`"mode":"continuous","enabled":true,"claims_enabled":true,` +
		`"configuration":{"targets":[{"provider":"github_actions",` +
		`"scope_id":"ci-cd:github-actions:example-org/example-repo",` +
		`"repository":"example-org/example-repo","auth_mode":"github_app",` +
		`"github_app_id_env":"APP_ID","github_app_installation_id_env":"INSTALL_ID",` +
		`"github_app_private_key_env":"APP_KEY",` +
		`"allowed_repositories":["example-org/example-repo"],` +
		`"api_base_url":"` + server.URL + `","max_runs":1,"max_jobs":25,"max_artifacts":25}]}}]`
	getenv := func(key string) string {
		switch key {
		case producer.EnvCollectorInstances:
			return instances
		case producer.EnvCollectorInstanceID:
			return "cicd-run-primary"
		case "APP_ID":
			return "123"
		case "INSTALL_ID":
			return "456"
		case "APP_KEY":
			return privatePEM
		default:
			return ""
		}
	}
	handler := testHandler(getenv, server)
	result, err := handler.Handle(context.Background(), handlerRequest(handlerClaim(), nil))
	if err != nil {
		t.Fatalf("Handle() error = %v, want nil", err)
	}
	if result.State != sdk.ResultTerminal {
		t.Fatalf("Handle() state = %q, want terminal", result.State)
	}
	if got := failureClass(t, result); got != "invalid_config.github_app_mint_failed" {
		t.Errorf("FailureClass = %q, want the distinct mint failure", got)
	}
	if len(result.Facts) != 0 {
		t.Errorf("Handle() facts = %d, want 0: a denied mint never emits", len(result.Facts))
	}
}

// A throttled mint releases the claim retryable, exactly like any other
// transient fetch fault — never a terminal for a fault that may clear.
func TestHandleGitHubAppThrottledMintIsRetryable(t *testing.T) {
	t.Parallel()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v, want nil", err)
	}
	privatePEM := string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}))
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/app/installations/456/access_tokens" {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "throttled", http.StatusTooManyRequests)
			return
		}
		t.Errorf("unexpected path %s: no provider read may precede a mint", r.URL.Path)
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)

	instances := `[{"instance_id":"cicd-run-primary","collector_kind":"ci_cd_run",` +
		`"mode":"continuous","enabled":true,"claims_enabled":true,` +
		`"configuration":{"targets":[{"provider":"github_actions",` +
		`"scope_id":"ci-cd:github-actions:example-org/example-repo",` +
		`"repository":"example-org/example-repo","auth_mode":"github_app",` +
		`"github_app_id_env":"APP_ID","github_app_installation_id_env":"INSTALL_ID",` +
		`"github_app_private_key_env":"APP_KEY",` +
		`"allowed_repositories":["example-org/example-repo"],` +
		`"api_base_url":"` + server.URL + `","max_runs":1,"max_jobs":25,"max_artifacts":25}]}}]`
	getenv := func(key string) string {
		switch key {
		case producer.EnvCollectorInstances:
			return instances
		case producer.EnvCollectorInstanceID:
			return "cicd-run-primary"
		case "APP_ID":
			return "123"
		case "INSTALL_ID":
			return "456"
		case "APP_KEY":
			return privatePEM
		default:
			return ""
		}
	}
	handler := testHandler(getenv, server)
	result, err := handler.Handle(context.Background(), handlerRequest(handlerClaim(), nil))
	if err != nil {
		t.Fatalf("Handle() error = %v, want nil", err)
	}
	if result.State != sdk.ResultRetryable {
		t.Fatalf("Handle() state = %q, want retryable for a throttled mint", result.State)
	}
	if len(result.Facts) != 0 {
		t.Errorf("Handle() facts = %d, want 0: retryable never emits", len(result.Facts))
	}
}

// An unloadable activation fails closed as activation, and the
// unexported outcome helpers keep their clamps.
func TestHandleBadActivationIsTerminal(t *testing.T) {
	t.Parallel()

	handler := Handler{Getenv: func(key string) string {
		if key == producer.EnvCollectorInstances {
			return "not-json"
		}
		return ""
	}}
	result, err := handler.Handle(context.Background(), handlerRequest(handlerClaim(), nil))
	if err != nil {
		t.Fatalf("Handle() error = %v, want nil", err)
	}
	if result.State != sdk.ResultTerminal {
		t.Fatalf("Handle() state = %q, want terminal", result.State)
	}
	if got := failureClass(t, result); got != "invalid_config.activation" {
		t.Errorf("FailureClass = %q, want invalid_config.activation", got)
	}
}

func TestRetryAfterSecondsClamps(t *testing.T) {
	t.Parallel()

	if got := retryAfterSeconds(0); got != 30 {
		t.Errorf("retryAfterSeconds(0) = %d, want the 30-second default", got)
	}
	if got := retryAfterSeconds(90 * time.Second); got != 90 {
		t.Errorf("retryAfterSeconds(90s) = %d, want 90", got)
	}
	if got := retryAfterSeconds(time.Hour); got != 300 {
		t.Errorf("retryAfterSeconds(1h) = %d, want the 300-second clamp", got)
	}
}

func TestClaimTokenRejectsEmptyStaticAndUnknownMode(t *testing.T) {
	t.Parallel()

	var handler Handler
	if _, err := handler.claimToken(context.Background(),
		producer.ResolvedTarget{AuthMode: producer.AuthModeTokenEnv}, nil); err == nil {
		t.Error("claimToken(empty static) = nil, want fail-closed")
	}
	if _, err := handler.claimToken(context.Background(),
		producer.ResolvedTarget{AuthMode: "oauth"}, nil); err == nil {
		t.Error("claimToken(unknown mode) = nil, want fail-closed")
	}
}

func TestFetchWindowRejectsUnknownProvider(t *testing.T) {
	t.Parallel()

	var handler Handler
	_, err := handler.fetchWindow(context.Background(), handlerClaim(),
		producer.ResolvedTarget{Provider: "bogus"}, "token")
	if err == nil {
		t.Fatal("fetchWindow(bogus provider) = nil, want fail-closed")
	}
	var terminal *producer.TerminalError
	if !errors.As(err, &terminal) || terminal.Reason != producer.ReasonActivation {
		t.Errorf("fetchWindow(bogus provider) error = %v, want terminal activation", err)
	}
}

func TestResultForFetchErrorCoversEscapes(t *testing.T) {
	t.Parallel()

	claim := handlerClaim()
	if got := resultForFetchError(claim, producer.RateLimitError{RetryAfter: time.Minute}); got.State != sdk.ResultRetryable {
		t.Errorf("bare RateLimitError state = %q, want retryable (defensive release)", got.State)
	}
	if got := resultForFetchError(claim, errors.New("unclassified")); got.State != sdk.ResultTerminal {
		t.Errorf("unclassified error state = %q, want fail-closed terminal", got.State)
	} else if got.Statuses[0].FailureClass != "invalid_config.activation" {
		t.Errorf("FailureClass = %q, want invalid_config.activation", got.Statuses[0].FailureClass)
	}
}
