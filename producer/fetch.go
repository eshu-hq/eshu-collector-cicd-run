// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// HostMintedTokenKey is the request-config key carrying the host-minted
// GitHub App installation token for one claim. The host sets it per claim
// as an object {token, scope_id} (extensionhost.injectHostToken); the
// satellite spends the Bearer [REDACTED] never reads the App private key. A missing
// key, an empty token, or a scope_id mismatch terminal-fails the claim.
const HostMintedTokenKey = "host_github_app_token" // #nosec G101 -- names the stdio config key, not a credential value.

// invalid_config failure reasons. Every terminal the satellite serves for a
// claim it cannot serve carries invalid_config.<reason>: the bounded reason
// names the wiring fault an operator can act on, and never carries tokens,
// credentials, or provider payload text.
const (
	// ReasonActivation marks an unloadable activation (bad instances JSON,
	// unknown auth mode, unresolvable static credential, removed
	// host_token_file, out-of-range bounds).
	ReasonActivation = "activation"
	// ReasonClaimBinding marks a claim that does not bind this producer
	// (collector kind, instance, generation, or fencing mismatch).
	ReasonClaimBinding = "claim_binding"
	// ReasonTargetScopeMismatch marks a claim scope with no registered
	// provider target.
	ReasonTargetScopeMismatch = "target_scope_mismatch"
	// ReasonMissingHostToken marks a host_minted claim whose request config
	// carries no host-minted token object.
	ReasonMissingHostToken = "missing_host_token"
	// ReasonEmptyHostToken marks a host_minted claim whose token object
	// carries a blank token.
	ReasonEmptyHostToken = "empty_host_token"
	// ReasonHostTokenShape marks a host_minted claim whose token object is
	// not the {token, scope_id} shape.
	ReasonHostTokenShape = "host_token_shape"
	// ReasonHostTokenScopeMismatch marks a host_minted claim whose token
	// scope_id does not equal the bound target's scope.
	ReasonHostTokenScopeMismatch = "host_token_scope_mismatch"
	// ReasonGitHubAppMintFailed marks a direct github_app claim whose
	// in-satellite installation-token mint failed. It stays distinct from
	// every host-token reason so a mint fault never reads as a host fault.
	ReasonGitHubAppMintFailed = "github_app_mint_failed"
	// ReasonProviderDenied marks a provider 401/403: the credential was
	// refused at claim scope.
	ReasonProviderDenied = "provider_denied"
	// ReasonProviderNotFound marks a provider 404: the repository, run, or
	// pipeline the claim names does not exist under this credential.
	ReasonProviderNotFound = "provider_not_found"
	// ReasonProviderRejected marks any other terminal provider status: a
	// deterministic refusal a retry cannot fix.
	ReasonProviderRejected = "provider_rejected"
)

// FailureClassInvalidConfig is the terminal bucket for claims the satellite
// cannot serve. It always carries a reason suffix; the bare bucket is never
// emitted.
const FailureClassInvalidConfig = "invalid_config"

// InvalidConfigClass returns the terminal failure class for a reason,
// defaulting a blank reason to activation.
func InvalidConfigClass(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = ReasonActivation
	}
	return FailureClassInvalidConfig + "." + reason
}

// TerminalError marks a deterministic claim failure: retrying cannot fix a
// wiring, credential, or scope fault. The Reason is bounded vocabulary; the
// Cause names keys and scopes, never credential values or payload text.
type TerminalError struct {
	Reason string
	Cause  error
}

func (e *TerminalError) Error() string {
	cause := ""
	if e.Cause != nil {
		cause = ": " + e.Cause.Error()
	}
	return "terminal " + InvalidConfigClass(e.Reason) + cause
}

func (e *TerminalError) Unwrap() error { return e.Cause }

// FailureClass returns the invalid_config.<reason> class this terminal
// serves under.
func (e *TerminalError) FailureClass() string { return InvalidConfigClass(e.Reason) }

// RetryableError marks a transient fetch failure: a transport fault, a
// 5xx, a malformed provider payload, or a first-request rate limit. The
// host releases the claim for bounded retry. RetryAfter carries provider
// retry guidance when the provider gave any.
type RetryableError struct {
	Cause      error
	RetryAfter time.Duration
}

func (e *RetryableError) Error() string {
	cause := ""
	if e.Cause != nil {
		cause = ": " + e.Cause.Error()
	}
	return "retryable transient_transport" + cause
}

func (e *RetryableError) Unwrap() error { return e.Cause }

// RateLimitError marks provider throttling observed mid-window with a
// partial page already committed. Unlike RetryableError it does not release
// the claim: the fetched prefix commits as a partial generation with a
// rate_limited warning.
type RateLimitError struct {
	RetryAfter time.Duration
}

func (e RateLimitError) Error() string { return "provider rate limited" }

// HTTPError carries a bounded provider non-2xx without response bodies,
// URLs, or credential values.
type HTTPError struct {
	Provider   string
	StatusCode int
	RetryAfter time.Duration
}

func (e HTTPError) Error() string {
	return fmt.Sprintf("%s provider request failed with status %d", e.Provider, e.StatusCode)
}

// ResolveHostToken spends one claim's host-minted token object. The token
// must be present, non-blank, and minted for the bound target's scope;
// anything else terminal-fails with the matching invalid_config reason.
func ResolveHostToken(config map[string]any, scopeID string) (string, error) {
	raw, ok := config[HostMintedTokenKey]
	if !ok || raw == nil {
		return "", &TerminalError{Reason: ReasonMissingHostToken, Cause: errors.New("request config carries no host-minted token")}
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return "", &TerminalError{Reason: ReasonHostTokenShape, Cause: errors.New("host-minted token is not a token object")}
	}
	tokenRaw, present := obj["token"]
	if !present {
		return "", &TerminalError{Reason: ReasonHostTokenShape, Cause: errors.New("host-minted token object carries no token field")}
	}
	token, ok := tokenRaw.(string)
	if !ok {
		return "", &TerminalError{Reason: ReasonHostTokenShape, Cause: errors.New("host-minted token field is not a string")}
	}
	if strings.TrimSpace(token) == "" {
		return "", &TerminalError{Reason: ReasonEmptyHostToken, Cause: errors.New("host-minted token is empty")}
	}
	tokenScope, _ := obj["scope_id"].(string)
	if strings.TrimSpace(tokenScope) != strings.TrimSpace(scopeID) {
		return "", &TerminalError{Reason: ReasonHostTokenScopeMismatch, Cause: errors.New("host-minted token scope does not match target scope")}
	}
	return strings.TrimSpace(token), nil
}

// defaultHTTPClient bounds provider reads the way the in-tree runtime does:
// a 30-second ceiling with the claim deadline (not the client) as the real
// fetch bound. The transport keeps proxy-from-environment so workers behind
// an egress proxy behave like every other Go HTTP client on the host.
func defaultHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

// getJSON issues one authenticated GET and decodes the JSON body into out.
// Transport failures and 5xx become *RetryableError; provider throttling
// becomes RateLimitError; other non-2xx become HTTPError. The error never
// carries response bodies or credential values.
func getJSON(ctx context.Context, client *http.Client, provider, endpoint string, setAuth func(*http.Request), out any, limited func(*http.Response) bool, retryAfter func(http.Header, time.Time) time.Duration) error {
	if client == nil {
		client = defaultHTTPClient()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return &RetryableError{Cause: err}
	}
	setAuth(request)
	response, err := client.Do(request)
	if err != nil {
		return &RetryableError{Cause: err}
	}
	defer func() {
		_ = response.Body.Close()
	}()
	if limited(response) {
		delay := retryAfter(response.Header, time.Now())
		if delay <= 0 {
			delay = time.Minute
		}
		return RateLimitError{RetryAfter: delay}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		httpErr := HTTPError{Provider: provider, StatusCode: response.StatusCode, RetryAfter: parseRetryAfterHeader(response.Header.Get("Retry-After"), time.Now())}
		if response.StatusCode >= 500 {
			return &RetryableError{Cause: httpErr}
		}
		return httpErr
	}
	decoder := json.NewDecoder(response.Body)
	decoder.UseNumber()
	if err := decoder.Decode(out); err != nil {
		return &RetryableError{Cause: fmt.Errorf("decode %s response: %w", provider, err)}
	}
	return nil
}

// classifyHTTPError maps a provider HTTPError onto the claim outcome: auth
// refusals and missing resources terminal-fail with their
// invalid_config reason, anything else deterministic terminal-fails as
// rejected. RateLimitError and RetryableError pass through untouched.
func classifyHTTPError(err error) error {
	var httpErr HTTPError
	if !errors.As(err, &httpErr) {
		return err
	}
	switch httpErr.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &TerminalError{Reason: ReasonProviderDenied, Cause: err}
	case http.StatusNotFound:
		return &TerminalError{Reason: ReasonProviderNotFound, Cause: err}
	default:
		return &TerminalError{Reason: ReasonProviderRejected, Cause: err}
	}
}

// joinURL resolves a provider path plus query onto a validated base URL.
// Only http and https bases are accepted; credentials in the base are
// rejected. The message names the field, never the URL value.
func joinURL(field, base, path string, query map[string]string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", field, err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", fmt.Errorf("%s must use http or https", field)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("%s must include scheme and host", field)
	}
	if parsed.User != nil {
		return "", fmt.Errorf("%s must not include credentials", field)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/"
	relative, err := url.Parse(strings.TrimLeft(path, "/"))
	if err != nil {
		return "", fmt.Errorf("parse %s path: %w", field, err)
	}
	joined := parsed.ResolveReference(relative)
	values := joined.Query()
	for key, value := range query {
		values.Set(key, value)
	}
	joined.RawQuery = values.Encode()
	return joined.String(), nil
}

// numericProviderID renders a provider id field (json.Number from a
// UseNumber decode, float64, int variants, or a pre-rendered string) into
// its canonical string. It ports the in-tree numericProviderID so fixture
// payloads and live API responses decode through the same seam.
func numericProviderID(value any) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "", errors.New("id is required")
	case json.Number:
		if strings.ContainsAny(typed.String(), ".eE") {
			return "", fmt.Errorf("id %q must be an integer", typed.String())
		}
		return typed.String(), nil
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed {
			return "", fmt.Errorf("id %v must be an integer", typed)
		}
		return strconv.FormatInt(int64(typed), 10), nil
	case int:
		return strconv.Itoa(typed), nil
	case int64:
		return strconv.FormatInt(typed, 10), nil
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return "", errors.New("id is required")
		}
		return trimmed, nil
	default:
		return "", fmt.Errorf("unsupported id shape %T", value)
	}
}

// anyString renders an optional provider field: nil and non-strings become
// "", matching the in-tree trim() leniency for string-typed fixture fields.
func anyString(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

// parseRetryAfterHeader parses a Retry-After value in delay-seconds or
// HTTP-date form. It ports the in-tree sdk.ParseRetryAfter contract to a
// stdlib-only helper: unparseable values yield zero.
func parseRetryAfterHeader(value string, now time.Time) time.Duration {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(trimmed); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if date, err := http.ParseTime(trimmed); err == nil {
		delay := date.Sub(now)
		if delay > 0 {
			return delay
		}
	}
	return 0
}
