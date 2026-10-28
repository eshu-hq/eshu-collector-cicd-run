// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"errors"
	"strings"
	"testing"
)

// ResolveHostToken is the per-claim host_minted credential contract: the
// host injects config.host_github_app_token as an object {token, scope_id}
// (extensionhost.injectHostToken), and the satellite spends the Bearer [REDACTED]
// ever reading the App private key. Every failure is a fail-closed terminal
// with a bounded invalid_config.<reason> class and no credential text.
func TestResolveHostTokenMatrix(t *testing.T) {
	t.Parallel()

	const scope = "ci-cd:github-actions:example-org/example-repo"

	full := map[string]any{
		HostMintedTokenKey: map[string]any{"token": "minted-canary", "scope_id": scope},
	}
	token, err := ResolveHostToken(full, scope)
	if err != nil {
		t.Fatalf("ResolveHostToken(full) error = %v, want nil", err)
	}
	if token != "minted-canary" {
		t.Fatalf("ResolveHostToken(full) = %q, want the minted token", token)
	}

	cases := []struct {
		name   string
		config map[string]any
		reason string
	}{
		{"missing key", map[string]any{}, ReasonMissingHostToken},
		{"nil value", map[string]any{HostMintedTokenKey: nil}, ReasonMissingHostToken},
		{"bare string", map[string]any{HostMintedTokenKey: "minted-canary"}, ReasonHostTokenShape},
		{"bare list", map[string]any{HostMintedTokenKey: []any{}}, ReasonHostTokenShape},
		{"missing token field", map[string]any{HostMintedTokenKey: map[string]any{"scope_id": scope}}, ReasonHostTokenShape},
		{"non-string token", map[string]any{HostMintedTokenKey: map[string]any{"token": 42, "scope_id": scope}}, ReasonHostTokenShape},
		{"empty token", map[string]any{HostMintedTokenKey: map[string]any{"token": "", "scope_id": scope}}, ReasonEmptyHostToken},
		{"blank token", map[string]any{HostMintedTokenKey: map[string]any{"token": "  ", "scope_id": scope}}, ReasonEmptyHostToken},
		{"missing scope", map[string]any{HostMintedTokenKey: map[string]any{"token": "minted-canary"}}, ReasonHostTokenScopeMismatch},
		{"scope mismatch", map[string]any{HostMintedTokenKey: map[string]any{"token": "minted-canary", "scope_id": "ci-cd:github-actions:other/repo"}}, ReasonHostTokenScopeMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := ResolveHostToken(tc.config, scope)
			if err == nil {
				t.Fatalf("ResolveHostToken(%s) = nil, want terminal %s", tc.name, tc.reason)
			}
			var terminal *TerminalError
			if !errors.As(err, &terminal) {
				t.Fatalf("ResolveHostToken(%s) error = %T, want *TerminalError", tc.name, err)
			}
			if terminal.Reason != tc.reason {
				t.Errorf("ResolveHostToken(%s) reason = %q, want %q", tc.name, terminal.Reason, tc.reason)
			}
			if got := terminal.FailureClass(); got != "invalid_config."+tc.reason {
				t.Errorf("FailureClass() = %q, want %q", got, "invalid_config."+tc.reason)
			}
			if strings.Contains(err.Error(), "minted-canary") {
				t.Errorf("ResolveHostToken(%s) error leaked the token: %q", tc.name, err.Error())
			}
		})
	}
}

// Terminal failures never carry credential or payload text: the reason is a
// bounded vocabulary word and the cause names keys, never values.
func TestTerminalErrorCarriesNoSecrets(t *testing.T) {
	t.Parallel()

	err := &TerminalError{Reason: ReasonProviderDenied, Cause: errors.New("provider refused the configured credential")}
	if got := err.FailureClass(); got != "invalid_config.provider_denied" {
		t.Fatalf("FailureClass() = %q, want invalid_config.provider_denied", got)
	}
	if strings.Contains(err.Error(), "ghs_") {
		t.Fatalf("TerminalError leaked credential text: %q", err.Error())
	}
}

// InvalidConfigClass always stays inside the invalid_config bucket.
func TestInvalidConfigClassDefaults(t *testing.T) {
	t.Parallel()

	if got := InvalidConfigClass(""); got != "invalid_config.activation" {
		t.Fatalf("InvalidConfigClass(blank) = %q, want invalid_config.activation", got)
	}
	if got := InvalidConfigClass(ReasonTargetScopeMismatch); got != "invalid_config.target_scope_mismatch" {
		t.Fatalf("InvalidConfigClass(scope) = %q, want invalid_config.target_scope_mismatch", got)
	}
}
