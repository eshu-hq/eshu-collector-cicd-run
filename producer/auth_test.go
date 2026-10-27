// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"strings"
	"testing"
)

// Auth-mode port proof (#8854, #9146 wiring from #9171): the satellite
// carries the App-auth credential shapes and fail-closed mode validation.
// Live minting and per-claim token resolution execute host-side (no live
// fetch ships here); these tests pin the shapes that unblock that fetch.

func TestLoadConfigResolvesGitHubAppReferences(t *testing.T) {
	t.Parallel()
	const privateKey = "private-key-value-canary"
	instanceJSON := `[{"instance_id":"cicd-app","collector_kind":"ci_cd_run","mode":"continuous","enabled":true,"claims_enabled":true,"configuration":{"targets":[{"provider":"github_actions","scope_id":"ci:example/repo","repository":"example/repo","allowed_repositories":["example/repo"],"auth_mode":"github_app","github_app_id_env":"APP_ID","github_app_installation_id_env":"INSTALL_ID","github_app_private_key_env":"PRIVATE_KEY","max_deployments":3}]}}]`
	getenv := func(key string) string {
		switch key {
		case EnvCollectorInstances:
			return instanceJSON
		case "APP_ID":
			return "123"
		case "INSTALL_ID":
			return "456"
		case "PRIVATE_KEY":
			return privateKey
		default:
			return ""
		}
	}
	config, err := LoadConfig(instanceJSON, "cicd-app", getenv)
	if err != nil {
		if strings.Contains(err.Error(), privateKey) {
			t.Fatalf("credential value leaked: %v", err)
		}
		t.Fatalf("valid App target rejected: %v", err)
	}
	if len(config.GitHubTargets) != 1 {
		t.Fatalf("GitHub target count = %d, want 1", len(config.GitHubTargets))
	}
	target := config.GitHubTargets[0]
	if target.MaxDeployments != 3 {
		t.Fatalf("MaxDeployments = %d, want configured bound 3", target.MaxDeployments)
	}
	if target.Token != "" {
		t.Fatal("static token unexpectedly set for App target")
	}
	if target.AuthMode != AuthModeGitHubApp {
		t.Fatalf("AuthMode = %q, want %q", target.AuthMode, AuthModeGitHubApp)
	}
	if target.GitHubAppID != "123" || target.GitHubAppInstallationID != "456" || target.GitHubAppPrivateKey != privateKey {
		t.Fatal("App env references were not resolved into runtime target")
	}
}

func TestLoadConfigRejectsUnresolvedAppEnv(t *testing.T) {
	t.Parallel()
	const config = `[{"instance_id":"cicd-app","collector_kind":"ci_cd_run","mode":"continuous","enabled":true,"claims_enabled":true,"configuration":{"targets":[{"provider":"github_actions","scope_id":"ci:example/repo","repository":"example/repo","allowed_repositories":["example/repo"],"auth_mode":"github_app","github_app_id_env":"APP_ID","github_app_installation_id_env":"INSTALL_ID","github_app_private_key_env":"PRIVATE_KEY"}]}}]`
	for _, test := range []struct{ missing, field string }{
		{"APP_ID", "github_app_id_env"},
		{"INSTALL_ID", "github_app_installation_id_env"},
		{"PRIVATE_KEY", "github_app_private_key_env"},
	} {
		t.Run(test.missing, func(t *testing.T) {
			t.Parallel()
			_, err := LoadConfig(config, "cicd-app", func(key string) string {
				if key == EnvCollectorInstances {
					return config
				}
				if key == test.missing {
					return ""
				}
				switch key {
				case "APP_ID", "INSTALL_ID", "PRIVATE_KEY":
					return "secret-value-canary"
				default:
					return ""
				}
			})
			if err == nil || !strings.Contains(err.Error(), test.field) || strings.Contains(err.Error(), "secret-value-canary") {
				t.Fatalf("load config error = %v, want named field without value", err)
			}
		})
	}
}

func TestMapGitHubTargetRejectsMixedCredentialModes(t *testing.T) {
	t.Parallel()
	base := targetJSON{ScopeID: "ci:example/repo", Repository: "example/repo", AllowedRepositories: []string{"example/repo"}}
	cases := []struct {
		name     string
		mode     string
		tokenEnv string
		hostFile string
		appIDEnv string
		want     string
	}{
		{"static with app", AuthModeTokenEnv, "TOKEN", "", "APP_ID", "github_app"},
		{"host with app", AuthModeHostMinted, "", "/token.json", "APP_ID", "github_app"},
		{"app with host", AuthModeGitHubApp, "", "/token.json", "APP_ID", "host_token_file"},
		{"app with static", AuthModeGitHubApp, "TOKEN", "", "APP_ID", "token_env"},
		{"unknown mode", "oauth", "TOKEN", "", "", "auth_mode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v := base
			v.AuthMode, v.TokenEnv, v.HostTokenFile, v.GitHubAppIDEnv = tc.mode, tc.tokenEnv, tc.hostFile, tc.appIDEnv
			v.GitHubAppInstallationIDEnv, v.GitHubAppPrivateKeyEnv = "INSTALL_ID", "KEY"
			_, err := mapGitHubTarget(v, func(string) string { return "credential-canary" })
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "credential-canary") {
				t.Fatalf("map target = %v, want %s without secret", err, tc.want)
			}
		})
	}
}

func TestAuthModeDefaultsToTokenEnv(t *testing.T) {
	t.Parallel()
	target := targetJSON{ScopeID: "ci:example/repo", Repository: "example/repo", TokenEnv: "TOKEN"}
	resolved, err := mapGitHubTarget(target, func(key string) string {
		if key == "TOKEN" {
			return "static-canary"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("omitted auth_mode rejected: %v", err)
	}
	if resolved.AuthMode != AuthModeTokenEnv || resolved.Token != "static-canary" {
		t.Fatalf("defaulted target = mode %q token %q, want token_env with resolved credential", resolved.AuthMode, resolved.Token)
	}
}

func TestMapGitHubTargetResolvesHostMintedPath(t *testing.T) {
	t.Parallel()
	host := targetJSON{ScopeID: "ci:example/repo", Repository: "example/repo", AuthMode: "host_minted", HostTokenFile: "/run/host/token.json"}
	resolved, err := mapGitHubTarget(host, func(string) string { return "" })
	if err != nil {
		t.Fatalf("valid host target rejected: %v", err)
	}
	if resolved.AuthMode != AuthModeHostMinted || resolved.HostTokenFile != "/run/host/token.json" || resolved.Token != "" {
		t.Fatalf("host target = mode %q file %q token %q, want carried path with empty token", resolved.AuthMode, resolved.HostTokenFile, resolved.Token)
	}
	if resolved.MaxDeployments != 10 {
		t.Fatalf("host target MaxDeployments = %d, want default 10", resolved.MaxDeployments)
	}
}

func TestNormalizeAuthMode(t *testing.T) {
	t.Parallel()
	if got := NormalizeAuthMode("GitHub_App"); got != AuthModeGitHubApp {
		t.Fatalf("NormalizeAuthMode(GitHub_App) = %q, want %q", got, AuthModeGitHubApp)
	}
	if got := NormalizeAuthMode(""); got != AuthModeTokenEnv {
		t.Fatalf("NormalizeAuthMode(blank) = %q, want %q", got, AuthModeTokenEnv)
	}
}

func TestMaxDeploymentsBound(t *testing.T) {
	t.Parallel()
	resolve := func(raw int) targetJSON {
		return targetJSON{ScopeID: "ci:example/repo", Repository: "example/repo", TokenEnv: "TOKEN", MaxDeployments: raw}
	}
	getenv := func(string) string { return "static-canary" }
	zeroed, err := mapGitHubTarget(resolve(0), getenv)
	if err != nil || zeroed.MaxDeployments != 10 {
		t.Fatalf("max_deployments 0 = %d, err = %v; want default 10", zeroed.MaxDeployments, err)
	}
	if _, err := mapGitHubTarget(resolve(101), getenv); err == nil {
		t.Error("max_deployments 101 = nil, want hard-cap rejection")
	}
	if _, err := mapGitHubTarget(resolve(-1), getenv); err == nil {
		t.Error("max_deployments -1 = nil, want rejection")
	}
}

func TestClassifyProviderStatusCode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		status int
		mode   string
		want   string
	}{
		{"static 401 stays generic", 401, AuthModeTokenEnv, StatusClassError},
		{"static 403 stays generic", 403, AuthModeTokenEnv, StatusClassError},
		{"app 401 is auth_error", 401, AuthModeGitHubApp, StatusClassAuthError},
		{"app 403 is auth_error", 403, AuthModeGitHubApp, StatusClassAuthError},
		{"app 404 is auth_error", 404, AuthModeGitHubApp, StatusClassAuthError},
		{"app 500 stays generic", 500, AuthModeGitHubApp, StatusClassError},
		{"host 401 is auth_denied", 401, AuthModeHostMinted, StatusClassAuthDenied},
		{"rate limit preserved", 429, AuthModeGitHubApp, StatusClassRateLimited},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ClassifyProviderStatusCode(tc.status, tc.mode); got != tc.want {
				t.Fatalf("status %d mode %q = %q, want %q", tc.status, tc.mode, got, tc.want)
			}
		})
	}
}
