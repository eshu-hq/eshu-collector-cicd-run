// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"net/url"
	"strings"
)

// stripSensitiveURL drops URLs that carry credentials or query strings
// before they reach a persisted source_ref or redacted free text. It
// mirrors envelope.go's in-tree guard: hierarchical URLs are dropped when
// url.Parse surfaces User or RawQuery; non-hierarchical (opaque) values
// are dropped when the authority spelling carries userinfo.
//
// OPEN GAP (P1): the opaque-userinfo predicate lives in-tree only
// (go/internal/urlredact.CarriesUserinfo, consulted by the published SDK
// as the unexported opaqueHasAuthority in
// github.com/eshu-hq/eshu-sdk/collector@v0.1.0-rc.3/validation.go:267).
// The SDK exports no equivalent, so this port approximates the opaque
// case with a conservative local check and relies on the SDK Validator as
// the fail-closed backstop (ValidateResult rejects any source_ref URI
// carrying credentials). If the SDK exports the predicate, adopt it and
// drop the local check.
func stripSensitiveURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	if parsed.User != nil || parsed.RawQuery != "" {
		return ""
	}
	if parsed.Host == "" && carriesOpaqueUserinfo(value) {
		// Not hierarchical, so User can be nil with the credential in plain
		// sight: an opaque `svc:SECRET@host/x` keeps it in Opaque, which a
		// naive check re-emits verbatim.
		return ""
	}
	return value
}

// carriesOpaqueUserinfo approximates urlredact.CarriesUserinfo for the
// opaque (non-hierarchical) URL shape: userinfo appears before the first
// path separator inside the opaque part. Conservative by design: an
// ambiguous value is dropped rather than persisted.
func carriesOpaqueUserinfo(raw string) bool {
	opaque := raw
	if i := strings.Index(opaque, ":"); i >= 0 {
		opaque = opaque[i+1:]
	}
	for _, cut := range []string{"/", "?", "#"} {
		if i := strings.Index(opaque, cut); i >= 0 {
			opaque = opaque[:i]
		}
	}
	return strings.Contains(opaque, "@")
}

// redactSensitiveText replaces credential- or query-bearing URLs inside
// free text with [redacted_url]. It mirrors the in-tree guard.
func redactSensitiveText(value string) string {
	fields := strings.Fields(value)
	for i, field := range fields {
		trimmed := strings.Trim(field, ".,;")
		if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
			if stripSensitiveURL(trimmed) == "" {
				fields[i] = strings.Replace(field, trimmed, "[redacted_url]", 1)
			}
		}
	}
	return strings.Join(fields, " ")
}

// sanitizeDeploymentURL strips the query string and fragment from a
// provider-supplied deployment URL before it is persisted.
// environment_url and log_url are set by whatever created the deployment,
// so they can carry a signed URL, an access token, or private path
// metadata. An unparseable value yields an empty string rather than being
// passed through unchecked.
func sanitizeDeploymentURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return ""
	}
	if parsed.Host == "" && carriesOpaqueUserinfo(trimmed) {
		// Not hierarchical, so User can be nil with a credential in plain
		// sight (`svc:SECRET@host/x` keeps it in Opaque, which String()
		// round-trips verbatim). Only an authority-shaped credential drops.
		return ""
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.User = nil
	return parsed.String()
}
