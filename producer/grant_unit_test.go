// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// roundTripFunc adapts a function to http.RoundTripper so mint tests can
// assert on the request without any network.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func mintTarget(baseURL, installationID, key string) ResolvedTarget {
	return ResolvedTarget{
		Provider:                ProviderGitHubActions,
		ScopeID:                 "ci-cd:github-actions:example-org/example-repo",
		Repository:              "example-org/example-repo",
		AuthMode:                AuthModeGitHubApp,
		APIBaseURL:              baseURL,
		GitHubAppID:             "123",
		GitHubAppInstallationID: installationID,
		GitHubAppPrivateKey:     key,
	}
}

func TestAuthorizeClaimBindingRejectsForeignKind(t *testing.T) {
	t.Parallel()

	config := loadSingleTargetConfig(t)
	claim := validSDKClaim(windowScope(), "gen-foreign-1")
	claim.CollectorKind = "other_kind"
	if _, err := authorizeClaimBinding(claim, config); err == nil {
		t.Error("authorizeClaimBinding(foreign kind) = nil, want rejection")
	}
}

func TestAuthorizeClaimBindingRequiresFencing(t *testing.T) {
	t.Parallel()

	config := loadSingleTargetConfig(t)
	claim := validSDKClaim(windowScope(), "gen-unfenced-1")
	claim.FencingToken = ""
	if _, err := authorizeClaimBinding(claim, config); !errors.Is(err, errMissingFencing) {
		t.Errorf("authorizeClaimBinding(no fencing) = %v, want errMissingFencing", err)
	}
}

func TestAuthorizeClaimBindingRequiresARouter(t *testing.T) {
	t.Parallel()

	config := Config{InstanceID: "lonely", CollectorKind: CollectorKind}
	claim := validSDKClaim("ci-cd:github-actions:example-org/example-repo", "gen-lonely-1")
	claim.InstanceID = "lonely"
	if _, err := authorizeClaimBinding(claim, config); err == nil {
		t.Error("authorizeClaimBinding(no targets) = nil, want the router rejection")
	}
}

func TestResolveClaimTargetBindsScope(t *testing.T) {
	t.Parallel()

	config := loadSingleTargetConfig(t)
	target, err := ResolveClaimTarget(validSDKClaim(windowScope(), "gen-resolve-1"), config)
	if err != nil {
		t.Fatalf("ResolveClaimTarget() error = %v, want nil", err)
	}
	if target.Provider != ProviderGitHubActions {
		t.Errorf("ResolveClaimTarget().Provider = %q, want github_actions", target.Provider)
	}
}

func TestMintClaimTokenSuccess(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/app/installations/456/access_tokens" {
			t.Errorf("mint path = %q, want the installation token endpoint", r.URL.EscapedPath())
		}
		_, _ = w.Write([]byte(`{"token":"minted"}`))
	}))
	t.Cleanup(server.Close)

	token, err := MintClaimToken(context.Background(), mintTarget(server.URL, "456", testPrivateKeyPEM(t)), server.Client())
	if err != nil {
		t.Fatalf("MintClaimToken() error = %v, want nil", err)
	}
	if token != "minted" {
		t.Errorf("MintClaimToken() = %q, want the minted token", token)
	}
}

func TestMintClaimTokenRejectsBlankCredential(t *testing.T) {
	t.Parallel()

	_, err := MintClaimToken(context.Background(), ResolvedTarget{}, nil)
	var terminal *TerminalError
	if !errors.As(err, &terminal) || terminal.Reason != ReasonGitHubAppMintFailed {
		t.Errorf("MintClaimToken(blank credential) = %v, want terminal github_app_mint_failed", err)
	}
}

func TestMintClaimTokenRejectsBadBaseURL(t *testing.T) {
	t.Parallel()

	_, err := MintClaimToken(context.Background(), mintTarget("://bad", "456", testPrivateKeyPEM(t)), nil)
	var terminal *TerminalError
	if !errors.As(err, &terminal) || terminal.Reason != ReasonGitHubAppMintFailed {
		t.Errorf("MintClaimToken(bad base URL) = %v, want terminal github_app_mint_failed", err)
	}
}

func TestMintClaimTokenDefaultsTheBaseURL(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.github.com" {
			t.Errorf("mint host = %q, want the default api.github.com", r.URL.Host)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"token":"defaulted"}`)),
			Header:     make(http.Header),
		}, nil
	})}
	token, err := MintClaimToken(context.Background(), mintTarget("", "456", testPrivateKeyPEM(t)), client)
	if err != nil {
		t.Fatalf("MintClaimToken(blank base URL) error = %v, want the default host", err)
	}
	if token != "defaulted" {
		t.Errorf("MintClaimToken() = %q, want the minted token", token)
	}
}

func TestMintClaimTokenUsesABoundedClientWhenNil(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"token":"bounded"}`))
	}))
	t.Cleanup(server.Close)

	token, err := MintClaimToken(context.Background(), mintTarget(server.URL, "456", testPrivateKeyPEM(t)), nil)
	if err != nil {
		t.Fatalf("MintClaimToken(nil client) error = %v, want nil", err)
	}
	if token != "bounded" {
		t.Errorf("MintClaimToken() = %q, want the minted token", token)
	}
}

func TestMintClaimTokenTransportFailureIsRetryable(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, boom
	})}
	_, err := MintClaimToken(context.Background(), mintTarget("https://api.github.com", "456", testPrivateKeyPEM(t)), client)
	var retryable *RetryableError
	if !errors.As(err, &retryable) || !errors.Is(err, boom) {
		t.Fatalf("MintClaimToken(transport failure) = %v, want the wrapped *RetryableError", err)
	}
}

func TestMintClaimTokenRedirectIsRejectedTerminal(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusMovedPermanently,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
		}, nil
	})}
	_, err := MintClaimToken(context.Background(), mintTarget("https://api.github.com", "456", testPrivateKeyPEM(t)), client)
	var terminal *TerminalError
	if !errors.As(err, &terminal) || terminal.Reason != ReasonGitHubAppMintFailed {
		t.Errorf("MintClaimToken(redirect) = %v, want terminal github_app_mint_failed", err)
	}
	if terminal != nil && !strings.Contains(terminal.Error(), "rejected") {
		t.Errorf("MintClaimToken(redirect) = %v, want the rejected class, not denied", err)
	}
}

func TestMintClaimTokenBadPayloadIsRetryable(t *testing.T) {
	t.Parallel()

	for _, body := range []string{`not json`, `{}`} {
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		})}
		_, err := MintClaimToken(context.Background(), mintTarget("https://api.github.com", "456", testPrivateKeyPEM(t)), client)
		var retryable *RetryableError
		if !errors.As(err, &retryable) {
			t.Errorf("MintClaimToken(%q) = %T, want *RetryableError", body, err)
		}
	}
}

func TestMintClaimTokenRejectsNonRSAKey(t *testing.T) {
	t.Parallel()

	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v, want nil", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(ecKey)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey() error = %v, want nil", err)
	}
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	_, err = MintClaimToken(context.Background(), mintTarget("https://api.github.com", "456", pemKey), nil)
	var terminal *TerminalError
	if !errors.As(err, &terminal) || terminal.Reason != ReasonGitHubAppMintFailed {
		t.Errorf("MintClaimToken(EC key) = %v, want terminal github_app_mint_failed", err)
	}
}
