// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// testPrivateKeyPEM generates one ephemeral RSA key per test: no private
// key material is ever committed, even test-only.
func testPrivateKeyPEM(t *testing.T) string {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v, want nil", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}))
}

// TestMintInstallationTokenSuccess proves the in-satellite direct-App mint:
// one RS256 JWT (iss=i App ID, 10-minute window) exchanged at the
// installation token endpoint, the same contract as the in-tree
// app.MintInstallationToken single attempt.
func TestMintInstallationTokenSuccess(t *testing.T) {
	t.Parallel()

	var gotAuth, gotPath string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		_, _ = w.Write([]byte(`{"token":"minted-installation-token"}`))
	}))
	t.Cleanup(server.Close)

	token, err := mintInstallationToken(context.Background(), server.URL, "123", "456", testPrivateKeyPEM(t), server.Client())
	if err != nil {
		t.Fatalf("mintInstallationToken() error = %v, want nil", err)
	}
	if token != "minted-installation-token" {
		t.Fatalf("mintInstallationToken() = %q, want the minted token", token)
	}
	if gotPath != "/app/installations/456/access_tokens" {
		t.Errorf("mint path = %q, want the installation token endpoint", gotPath)
	}
	bearer, ok := strings.CutPrefix(gotAuth, "Bearer ")
	if !ok {
		t.Fatalf("Authorization = %q, want a Bearer JWT", gotAuth)
	}
	segments := strings.Split(bearer, ".")
	if len(segments) != 3 {
		t.Fatalf("JWT segments = %d, want 3", len(segments))
	}
	claimsRaw, err := base64.RawURLEncoding.DecodeString(segments[1])
	if err != nil {
		t.Fatalf("decode JWT claims: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(claimsRaw, &claims); err != nil {
		t.Fatalf("decode JWT claims JSON: %v", err)
	}
	if claims["iss"] != "123" {
		t.Errorf("JWT iss = %v, want the App ID", claims["iss"])
	}
	iat, _ := claims["iat"].(float64)
	exp, _ := claims["exp"].(float64)
	if exp-iat != 600 {
		t.Errorf("JWT window = %v, want the 600-second mint window", exp-iat)
	}
}

func TestMintInstallationTokenFailuresAreDistinct(t *testing.T) {
	t.Parallel()

	key := testPrivateKeyPEM(t)
	for _, test := range []struct {
		name   string
		status int
		body   string
		// wantRetryable selects *RetryableError; otherwise the mint must
		// fail closed with the distinct github_app_mint_failed terminal.
		wantRetryable bool
	}{
		{"denied credential", http.StatusUnauthorized, "bad credentials", false},
		{"missing installation", http.StatusNotFound, "not found", false},
		{"transient is retryable", http.StatusBadGateway, "upstream fault", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, test.body+" mentions secret-canary", test.status)
			}))
			t.Cleanup(server.Close)

			_, err := mintInstallationToken(context.Background(), server.URL, "123", "456", key, server.Client())
			if err == nil {
				t.Fatalf("mintInstallationToken(%d) = nil, want a classified failure", test.status)
			}
			if strings.Contains(err.Error(), "secret-canary") {
				t.Fatalf("mint error leaked the provider body: %q", err.Error())
			}
			if test.wantRetryable {
				var retryable *RetryableError
				if !errors.As(err, &retryable) {
					t.Fatalf("mintInstallationToken(%d) error = %T, want *RetryableError", test.status, err)
				}
				return
			}
			var terminal *TerminalError
			if !errors.As(err, &terminal) {
				t.Fatalf("mintInstallationToken(%d) error = %T, want *TerminalError", test.status, err)
			}
			if terminal.Reason != ReasonGitHubAppMintFailed {
				t.Errorf("reason = %q, want the distinct %q", terminal.Reason, ReasonGitHubAppMintFailed)
			}
		})
	}
}

func TestMintInstallationTokenKeyShapes(t *testing.T) {
	t.Parallel()

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v, want nil", err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey() error = %v, want nil", err)
	}
	pkcs8PEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}))
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey(EC) error = %v, want nil", err)
	}
	ecPKCS8, err := x509.MarshalPKCS8PrivateKey(ecKey)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey(EC) error = %v, want nil", err)
	}
	ecPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: ecPKCS8}))

	if _, err := parseAppPrivateKey(pkcs8PEM); err != nil {
		t.Errorf("parseAppPrivateKey(PKCS8 RSA) error = %v, want nil", err)
	}
	if _, err := parseAppPrivateKey(ecPEM); err == nil {
		t.Error("parseAppPrivateKey(PKCS8 EC) = nil, want fail-closed non-RSA rejection")
	}
}

func TestMintInstallationTokenThrottleIsRetryable(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "throttled", http.StatusTooManyRequests)
	}))
	t.Cleanup(server.Close)

	_, err := mintInstallationToken(context.Background(), server.URL, "123", "456", testPrivateKeyPEM(t), server.Client())
	var retryable *RetryableError
	if !errors.As(err, &retryable) {
		t.Fatalf("mintInstallationToken(429) error = %T, want *RetryableError", err)
	}
	if retryable.RetryAfter <= 0 {
		t.Errorf("RetryAfter = %v, want the provider's 60-second guidance", retryable.RetryAfter)
	}
}

func TestMintInstallationTokenRejectsBadKey(t *testing.T) {
	t.Parallel()

	_, err := mintInstallationToken(context.Background(), "https://api.github.com", "123", "456", "not-a-pem-key", nil)
	if err == nil {
		t.Fatal("mintInstallationToken(bad key) = nil, want fail-closed")
	}
	var terminal *TerminalError
	if !errors.As(err, &terminal) {
		t.Fatalf("mintInstallationToken(bad key) error = %T, want *TerminalError", err)
	}
	if terminal.Reason != ReasonGitHubAppMintFailed {
		t.Errorf("reason = %q, want %q", terminal.Reason, ReasonGitHubAppMintFailed)
	}
	if strings.Contains(err.Error(), "not-a-pem-key") {
		t.Errorf("mint error echoed key material: %q", err.Error())
	}
}
