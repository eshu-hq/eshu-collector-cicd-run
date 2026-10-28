// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// mintInstallationToken exchanges direct github_app credentials for one
// installation token inside the satellite. It ports the single-attempt
// contract of the in-tree app.MintInstallationToken (RS256 JWT with
// iat=now-60s for clock skew and a 10-minute window, posted to the
// installation token endpoint) to a stdlib-only helper: the satellite
// module admits no JWT dependency. There is no in-call retry: a
// retryable mint fault releases the claim and the host's attempt budget
// decides the next mint. Every failure is bounded vocabulary with no
// credential, key, token, or JWT text.
func mintInstallationToken(ctx context.Context, baseURL, appID, installationID, privateKey string, client *http.Client) (string, error) {
	appID = strings.TrimSpace(appID)
	installationID = strings.TrimSpace(installationID)
	if appID == "" || strings.TrimSpace(installationID) == "" || strings.TrimSpace(privateKey) == "" {
		return "", &TerminalError{Reason: ReasonGitHubAppMintFailed, Cause: errors.New("github app mint requires an app id, an installation id, and a private key")}
	}
	key, err := parseAppPrivateKey(privateKey)
	if err != nil {
		return "", &TerminalError{Reason: ReasonGitHubAppMintFailed, Cause: err}
	}
	signed, err := signAppJWT(key, appID, time.Now())
	if err != nil {
		return "", &TerminalError{Reason: ReasonGitHubAppMintFailed, Cause: err}
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://api.github.com"
	}
	mintURL, err := joinURL("api_base_url", baseURL, "/app/installations/"+installationID+"/access_tokens", nil)
	if err != nil {
		return "", &TerminalError{Reason: ReasonGitHubAppMintFailed, Cause: err}
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, mintURL, nil)
	if err != nil {
		return "", &RetryableError{Cause: err}
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Authorization", "Bearer "+signed)
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := client.Do(request)
	if err != nil {
		return "", &RetryableError{Cause: err}
	}
	defer func() {
		_ = response.Body.Close()
	}()
	if response.StatusCode == http.StatusTooManyRequests || githubRateLimited(response) {
		return "", &RetryableError{
			Cause:      errors.New("github app mint rate limited"),
			RetryAfter: githubRetryAfter(response.Header, time.Now()),
		}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		denied := response.StatusCode == http.StatusUnauthorized ||
			response.StatusCode == http.StatusNotFound ||
			response.StatusCode == http.StatusForbidden ||
			(response.StatusCode >= 400 && response.StatusCode < 500)
		if response.StatusCode >= 500 {
			return "", &RetryableError{Cause: fmt.Errorf("github app mint failed with status %d", response.StatusCode)}
		}
		if denied {
			return "", &TerminalError{Reason: ReasonGitHubAppMintFailed, Cause: fmt.Errorf("github app mint denied with status %d", response.StatusCode)}
		}
		return "", &TerminalError{Reason: ReasonGitHubAppMintFailed, Cause: fmt.Errorf("github app mint rejected with status %d", response.StatusCode)}
	}
	var decoded struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return "", &RetryableError{Cause: errors.New("decode github app mint response")}
	}
	if strings.TrimSpace(decoded.Token) == "" {
		return "", &RetryableError{Cause: errors.New("github app mint returned no token")}
	}
	return strings.TrimSpace(decoded.Token), nil
}

// parseAppPrivateKey parses a PKCS#1 or PKCS#8 PEM-encoded RSA private key.
// The error names the failure, never key material.
func parseAppPrivateKey(raw string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(strings.TrimSpace(raw)))
	if block == nil {
		return nil, errors.New("parse github app private key: not PEM-encoded")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("parse github app private key: not RSA")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("parse github app private key: not RSA")
	}
	return key, nil
}

// signAppJWT signs the App JWT (iss=appID, iat=now-60s, exp=now+540s)
// with RS256 over base64url(header).base64url(claims).
func signAppJWT(key *rsa.PrivateKey, appID string, now time.Time) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(
		`{"iat":%d,"exp":%d,"iss":%q}`, now.Unix()-60, now.Unix()+540, appID)))
	message := header + "." + claims
	digest := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", errors.New("sign github app JWT")
	}
	return message + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}
