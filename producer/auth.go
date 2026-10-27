// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"fmt"
	"strings"
)

const (
	// AuthModeTokenEnv selects a static Bearer token resolved at startup.
	AuthModeTokenEnv = "token_env"
	// AuthModeHostMinted selects the host token file resolved on each claim.
	AuthModeHostMinted = "host_minted"
	// AuthModeGitHubApp selects a fresh installation token per claim.
	AuthModeGitHubApp = "github_app"
)

// NormalizeAuthMode lowercases and trims an operator auth_mode value. A
// blank value selects AuthModeTokenEnv, preserving the pre-App operator
// shape. It mirrors the in-tree validateTarget normalization.
func NormalizeAuthMode(mode string) string {
	normalized := strings.ToLower(strings.TrimSpace(mode))
	if normalized == "" {
		return AuthModeTokenEnv
	}
	return normalized
}

// ValidateGitHubAuth enforces the fail-closed credential-shape contract
// for one resolved github_actions target: exactly one credential shape
// per auth mode, never mixed. It ports validateTargetAuth from the
// in-tree runtime; only the live resolution (installation-token mint,
// host file read) executes host-side, so this validator runs on the
// resolved strings the config loader already carried.
func ValidateGitHubAuth(mode, token, hostFile, appID, installationID, privateKey string) error {
	switch NormalizeAuthMode(mode) {
	case AuthModeTokenEnv:
		if hostFile != "" {
			return fmt.Errorf("host_token_file must be empty when auth_mode is %q", AuthModeTokenEnv)
		}
		if token == "" {
			return fmt.Errorf("token is required")
		}
		if appID != "" || installationID != "" || privateKey != "" {
			return fmt.Errorf("github_app credentials must be empty when auth_mode is %q", AuthModeTokenEnv)
		}
	case AuthModeHostMinted:
		if hostFile == "" {
			return fmt.Errorf("host_token_file is required")
		}
		if token != "" {
			return fmt.Errorf("token must be empty when auth_mode is %q", AuthModeHostMinted)
		}
		if appID != "" || installationID != "" || privateKey != "" {
			return fmt.Errorf("github_app credentials must be empty when auth_mode is %q", AuthModeHostMinted)
		}
	case AuthModeGitHubApp:
		if hostFile != "" {
			return fmt.Errorf("host_token_file must be empty when auth_mode is %q", AuthModeGitHubApp)
		}
		if token != "" {
			return fmt.Errorf("token must be empty when auth_mode is %q", AuthModeGitHubApp)
		}
		if strings.TrimSpace(appID) == "" {
			return fmt.Errorf("github_app_id is required")
		}
		if strings.TrimSpace(installationID) == "" {
			return fmt.Errorf("github_app_installation_id is required")
		}
		if strings.TrimSpace(privateKey) == "" {
			return fmt.Errorf("github_app_private_key is required")
		}
	default:
		return fmt.Errorf("unsupported auth_mode %q", mode)
	}
	return nil
}

// hasAppEnvRefs reports whether a target declares any GitHub App env
// reference. It ports the in-tree hasAppRefs shape check to the
// satellite's env-reference field names.
func hasAppEnvRefs(target targetJSON) bool {
	return strings.TrimSpace(target.GitHubAppIDEnv) != "" ||
		strings.TrimSpace(target.GitHubAppInstallationIDEnv) != "" ||
		strings.TrimSpace(target.GitHubAppPrivateKeyEnv) != ""
}

// resolveAppCredentialRef resolves one App credential env reference. The
// error names the field and env reference, never the value. It ports the
// in-tree resolveAppCredentialEnv.
func resolveAppCredentialRef(getenv func(string) string, field, envName string) (string, error) {
	envName = strings.TrimSpace(envName)
	if envName == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	if value := strings.TrimSpace(getenv(envName)); value != "" {
		return value, nil
	}
	return "", fmt.Errorf("%s %s did not resolve a credential", field, envName)
}

// ClassifyProviderStatusCode maps one provider HTTP status to the
// MetricProviderRequests status_class for the target's auth mode. It
// ports classifyProviderStatusForMode from the in-tree runtime to a
// stdlib-only pure function the host fetch loop calls: direct-App
// 401/403/404 reads are auth_error, host-denied 401 refusals are
// auth_denied, rate limiting stays rate_limited, and every other status
// — including static-token failures — keeps the generic error class.
// Emission of the signal stays host-side; this only locks the mapping.
func ClassifyProviderStatusCode(statusCode int, authMode string) string {
	if statusCode == 429 {
		return StatusClassRateLimited
	}
	if NormalizeAuthMode(authMode) == AuthModeGitHubApp &&
		(statusCode == 401 || statusCode == 403 || statusCode == 404) {
		return StatusClassAuthError
	}
	if NormalizeAuthMode(authMode) == AuthModeHostMinted && statusCode == 401 {
		return StatusClassAuthDenied
	}
	return StatusClassError
}
