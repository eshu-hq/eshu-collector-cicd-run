// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func allowAllResponses(*http.Response) bool { return false }

func noRetryAfter(http.Header, time.Time) time.Duration { return 0 }

func TestDefaultHTTPClientBoundsReads(t *testing.T) {
	t.Parallel()

	if got := defaultHTTPClient(); got.Timeout != 30*time.Second {
		t.Errorf("defaultHTTPClient().Timeout = %v, want the 30-second claim-deadline ceiling", got.Timeout)
	}
}

func TestGetJSONUsesTheDefaultClientWhenNil(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)

	var decoded map[string]any
	if err := getJSON(context.Background(), nil, "github_actions", server.URL,
		func(*http.Request) {}, &decoded, allowAllResponses, noRetryAfter); err != nil {
		t.Fatalf("getJSON(nil client) error = %v, want the default-client fetch to succeed", err)
	}
	if decoded["ok"] != true {
		t.Errorf("getJSON(nil client) decoded = %v, want the server payload", decoded)
	}
}

func TestGetJSONRejectsBadEndpoint(t *testing.T) {
	t.Parallel()

	var decoded map[string]any
	err := getJSON(context.Background(), http.DefaultClient, "github_actions", "://bad",
		func(*http.Request) {}, &decoded, allowAllResponses, noRetryAfter)
	var retryable *RetryableError
	if !errors.As(err, &retryable) {
		t.Fatalf("getJSON(bad endpoint) error = %T, want *RetryableError", err)
	}
}

func TestGetJSONTransportFailureIsRetryable(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	endpoint := server.URL
	client := server.Client()
	server.Close()

	var decoded map[string]any
	err := getJSON(context.Background(), client, "github_actions", endpoint,
		func(*http.Request) {}, &decoded, allowAllResponses, noRetryAfter)
	var retryable *RetryableError
	if !errors.As(err, &retryable) {
		t.Fatalf("getJSON(closed server) error = %T, want *RetryableError", err)
	}
}

func TestGetJSONRateLimitDefaultsToOneMinute(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)

	limited := func(*http.Response) bool { return true }
	var decoded map[string]any
	err := getJSON(context.Background(), server.Client(), "github_actions", server.URL,
		func(*http.Request) {}, &decoded, limited, noRetryAfter)
	var rateLimited RateLimitError
	if !errors.As(err, &rateLimited) {
		t.Fatalf("getJSON(throttled) error = %T, want RateLimitError", err)
	}
	if rateLimited.RetryAfter != time.Minute {
		t.Errorf("RetryAfter = %v, want the one-minute default when the provider names none", rateLimited.RetryAfter)
	}
}

func TestGetJSONMalformedPayloadIsRetryable(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	t.Cleanup(server.Close)

	var decoded map[string]any
	err := getJSON(context.Background(), server.Client(), "github_actions", server.URL,
		func(*http.Request) {}, &decoded, allowAllResponses, noRetryAfter)
	var retryable *RetryableError
	if !errors.As(err, &retryable) {
		t.Fatalf("getJSON(malformed payload) error = %T, want *RetryableError", err)
	}
}

func TestClassifyHTTPErrorPassesThroughNonHTTP(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	if err := classifyHTTPError(boom); !errors.Is(err, boom) {
		t.Errorf("classifyHTTPError(non-HTTP) = %v, want the error untouched", err)
	}
	if err := classifyHTTPError(nil); err != nil {
		t.Errorf("classifyHTTPError(nil) = %v, want nil", err)
	}
}

func TestJoinURLMatrix(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		base    string
		path    string
		wantErr string
	}{
		{"unparseable base", "://bad", "/x", "parse api_base_url"},
		{"non-http scheme", "ftp://example.com", "/x", "must use http or https"},
		{"missing host", "https://", "/x", "must include scheme and host"},
		{"embedded credentials", "https://user:pass@example.com", "/x", "must not include credentials"},
		{"unparseable path", "https://example.com", "\x7f", "parse api_base_url path"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if _, err := joinURL("api_base_url", test.base, test.path, nil); err == nil {
				t.Errorf("joinURL(%q, %q) = nil, want rejection", test.base, test.path)
			} else if !strings.Contains(err.Error(), test.wantErr) {
				t.Errorf("joinURL error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestJoinURLResolvesPathAndQuery(t *testing.T) {
	t.Parallel()

	got, err := joinURL("api_base_url", "https://example.com/base", "items",
		map[string]string{"per_page": "100"})
	if err != nil {
		t.Fatalf("joinURL() error = %v, want nil", err)
	}
	if got != "https://example.com/base/items?per_page=100" {
		t.Errorf("joinURL() = %q, want the resolved path plus query", got)
	}
}

func TestNumericProviderIDMatrix(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		value   any
		want    string
		wantErr bool
	}{
		{"integer number", json.Number("7700"), "7700", false},
		{"float number", json.Number("7.5"), "", true},
		{"integer float", float64(42), "42", false},
		{"fractional float", float64(4.5), "", true},
		{"int", 7, "7", false},
		{"int64", int64(7700), "7700", false},
		{"trimmed string", "  7700  ", "7700", false},
		{"blank string", "   ", "", true},
		{"unsupported shape", true, "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := numericProviderID(test.value)
			if test.wantErr {
				if err == nil {
					t.Errorf("numericProviderID(%v) = nil, want rejection", test.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("numericProviderID(%v) error = %v, want nil", test.value, err)
			}
			if got != test.want {
				t.Errorf("numericProviderID(%v) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}

func TestParseRetryAfterHeaderMatrix(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	if got := parseRetryAfterHeader("120", now); got != 2*time.Minute {
		t.Errorf("parseRetryAfterHeader(120s) = %v, want 2m", got)
	}
	future := now.Add(2 * time.Minute).UTC().Format(http.TimeFormat)
	if got := parseRetryAfterHeader(future, now); got <= 0 || got > 2*time.Minute+time.Second {
		t.Errorf("parseRetryAfterHeader(future date) = %v, want about 2m", got)
	}
	for _, raw := range []string{
		"",
		"-5",
		"not-a-duration",
		now.Add(-time.Hour).UTC().Format(http.TimeFormat),
	} {
		if got := parseRetryAfterHeader(raw, now); got != 0 {
			t.Errorf("parseRetryAfterHeader(%q) = %v, want zero", raw, got)
		}
	}
}

func TestErrorTextNeverCarriesSecrets(t *testing.T) {
	t.Parallel()

	terminal := &TerminalError{Reason: ReasonActivation}
	if !strings.Contains(terminal.Error(), "invalid_config.activation") {
		t.Errorf("TerminalError.Error() = %q, want the bounded class", terminal.Error())
	}
	if got := (&RetryableError{}).Error(); got != "retryable transient_transport" {
		t.Errorf("RetryableError.Error() = %q, want the bare transient class", got)
	}
	if got := (RateLimitError{}).Error(); got == "" {
		t.Error("RateLimitError.Error() is empty, want bounded vocabulary")
	}
}
