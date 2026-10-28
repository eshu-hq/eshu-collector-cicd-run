// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"encoding/json"
	"fmt"
	"strings"
)

// splitTargets decodes the instance's target list and splits it into
// GitHub Actions and GitLab CI targets by each target's declared
// provider. An omitted or blank provider defaults to github_actions. An
// unrecognized provider fails loudly: acceptance of an unknown provider
// would silently drop collection scope.
func splitTargets(instance desiredInstance, getenv func(string) string) ([]ResolvedTarget, []ResolvedTarget, error) {
	var decoded struct {
		Targets []targetJSON `json:"targets"`
	}
	if err := json.Unmarshal([]byte(instance.Configuration), &decoded); err != nil {
		return nil, nil, fmt.Errorf("decode ci/cd run collector configuration: %w", err)
	}
	githubTargets := make([]ResolvedTarget, 0, len(decoded.Targets))
	gitlabTargets := make([]ResolvedTarget, 0, len(decoded.Targets))
	for i, target := range decoded.Targets {
		switch provider := strings.TrimSpace(target.Provider); provider {
		case "", string(ProviderGitHubActions):
			mapped, err := mapGitHubTarget(target, getenv)
			if err != nil {
				return nil, nil, fmt.Errorf("targets[%d]: %w", i, err)
			}
			githubTargets = append(githubTargets, mapped)
		case string(ProviderGitLabCI):
			mapped, err := mapGitLabTarget(target, getenv)
			if err != nil {
				return nil, nil, fmt.Errorf("targets[%d]: %w", i, err)
			}
			gitlabTargets = append(gitlabTargets, mapped)
		default:
			return nil, nil, fmt.Errorf("targets[%d]: unsupported ci/cd run provider %q", i, target.Provider)
		}
	}
	return githubTargets, gitlabTargets, nil
}

// mapGitHubTarget resolves one github_actions target. The credential
// resolves from the environment at startup into runtime-only fields; the
// error names the env reference, never the value. It ports the in-tree
// mapTarget auth-mode branching: exactly one credential shape per mode,
// fail-closed on any mix. host_token_file is retired (#9337): any
// configured value fails closed with removal guidance, in every mode.
// host_minted resolves no credential at load (the per-claim token arrives
// on the request config); direct github_app resolves the App triple for
// the in-satellite per-claim mint. Unknown modes fail closed.
func mapGitHubTarget(target targetJSON, getenv func(string) string) (ResolvedTarget, error) {
	mode := NormalizeAuthMode(target.AuthMode)
	tokenEnv := strings.TrimSpace(target.TokenEnv)
	if strings.TrimSpace(target.HostTokenFile) != "" {
		return ResolvedTarget{}, fmt.Errorf("host_token_file was removed: host_minted credentials arrive per claim on the request config; delete host_token_file from the activation")
	}
	if mode == AuthModeHostMinted {
		if hasAppEnvRefs(target) {
			return ResolvedTarget{}, fmt.Errorf("github_app env references must be empty when auth_mode is %q", mode)
		}
		if tokenEnv != "" {
			return ResolvedTarget{}, fmt.Errorf("auth_mode %q must not set token_env %q: the two credential shapes never mix", mode, tokenEnv)
		}
		return resolvedGitHubTarget(target, mode, "", "", "", "")
	}
	switch mode {
	case AuthModeTokenEnv:
		if hasAppEnvRefs(target) {
			return ResolvedTarget{}, fmt.Errorf("github_app env references must be empty when auth_mode is %q", mode)
		}
		token := ""
		if tokenEnv != "" {
			token = strings.TrimSpace(getenv(tokenEnv))
		}
		if token == "" {
			return ResolvedTarget{}, fmt.Errorf("token_env %s did not resolve a credential", tokenEnv)
		}
		return resolvedGitHubTarget(target, mode, token, "", "", "")
	case AuthModeGitHubApp:
		if tokenEnv != "" {
			return ResolvedTarget{}, fmt.Errorf("token_env must be empty when auth_mode is %q", mode)
		}
		appID, err := resolveAppCredentialRef(getenv, "github_app_id_env", target.GitHubAppIDEnv)
		if err != nil {
			return ResolvedTarget{}, err
		}
		installationID, err := resolveAppCredentialRef(getenv, "github_app_installation_id_env", target.GitHubAppInstallationIDEnv)
		if err != nil {
			return ResolvedTarget{}, err
		}
		privateKey, err := resolveAppCredentialRef(getenv, "github_app_private_key_env", target.GitHubAppPrivateKeyEnv)
		if err != nil {
			return ResolvedTarget{}, err
		}
		return resolvedGitHubTarget(target, mode, "", appID, installationID, privateKey)
	default:
		return ResolvedTarget{}, fmt.Errorf("unsupported ci/cd run auth_mode %q", target.AuthMode)
	}
}

// resolvedGitHubTarget builds the validated ResolvedTarget for one
// github_actions target after its credential shape resolved. It runs the
// fail-closed shape validator over the resolved strings, mirroring the
// in-tree validateTarget layering.
func resolvedGitHubTarget(target targetJSON, mode, token, appID, installationID, privateKey string) (ResolvedTarget, error) {
	if err := ValidateGitHubAuth(mode, token, appID, installationID, privateKey); err != nil {
		return ResolvedTarget{}, err
	}
	repository := strings.Trim(target.Repository, "/")
	maxRuns, err := resolveMaxRuns(target.MaxRuns)
	if err != nil {
		return ResolvedTarget{}, err
	}
	maxJobs, err := resolveMaxJobs(target.MaxJobs)
	if err != nil {
		return ResolvedTarget{}, err
	}
	maxArtifacts, err := resolveMaxArtifacts(target.MaxArtifacts)
	if err != nil {
		return ResolvedTarget{}, err
	}
	maxDeployments, err := resolveMaxDeployments(target.MaxDeployments)
	if err != nil {
		return ResolvedTarget{}, err
	}
	return ResolvedTarget{
		Provider:                ProviderGitHubActions,
		ScopeID:                 strings.TrimSpace(target.ScopeID),
		Repository:              repository,
		Token:                   token,
		AuthMode:                mode,
		GitHubAppID:             appID,
		GitHubAppInstallationID: installationID,
		GitHubAppPrivateKey:     privateKey,
		Allowed:                 cleanStrings(target.AllowedRepositories),
		APIBaseURL:              strings.TrimRight(strings.TrimSpace(target.APIBaseURL), "/"),
		SourceURI:               strings.TrimSpace(firstNonBlank(target.SourceURI, "https://github.com/"+repository)),
		MaxRuns:                 maxRuns,
		MaxJobs:                 maxJobs,
		MaxArtifacts:            maxArtifacts,
		MaxDeployments:          maxDeployments,
	}, nil
}

// mapGitLabTarget resolves one gitlab_ci target. GitLab targets serve the
// token_env shape only: host_minted and github_app name GitHub App
// machinery the GitLab fetch cannot spend, so any other auth_mode fails
// closed instead of silently serving the wrong shape. MaxArtifacts has no
// GitLab counterpart (job artifacts report inline) and is silently
// ignored rather than rejected.
func mapGitLabTarget(target targetJSON, getenv func(string) string) (ResolvedTarget, error) {
	if mode := NormalizeAuthMode(target.AuthMode); mode != AuthModeTokenEnv {
		return ResolvedTarget{}, fmt.Errorf("unsupported ci/cd run auth_mode %q for gitlab_ci: only token_env is supported", target.AuthMode)
	}
	if strings.TrimSpace(target.HostTokenFile) != "" {
		return ResolvedTarget{}, fmt.Errorf("host_token_file was removed: delete host_token_file from the activation")
	}
	tokenEnv := strings.TrimSpace(target.TokenEnv)
	token := ""
	if tokenEnv != "" {
		token = strings.TrimSpace(getenv(tokenEnv))
	}
	if token == "" {
		return ResolvedTarget{}, fmt.Errorf("token_env %s did not resolve a credential", tokenEnv)
	}
	repository := strings.Trim(target.Repository, "/")
	maxRuns, err := resolveMaxRuns(target.MaxRuns)
	if err != nil {
		return ResolvedTarget{}, err
	}
	maxJobs, err := resolveMaxJobs(target.MaxJobs)
	if err != nil {
		return ResolvedTarget{}, err
	}
	return ResolvedTarget{
		Provider:   ProviderGitLabCI,
		ScopeID:    strings.TrimSpace(target.ScopeID),
		Repository: repository,
		Token:      token,
		AuthMode:   AuthModeTokenEnv,
		Allowed:    cleanStrings(target.AllowedRepositories),
		APIBaseURL: strings.TrimRight(strings.TrimSpace(target.APIBaseURL), "/"),
		SourceURI:  strings.TrimSpace(firstNonBlank(target.SourceURI, "https://gitlab.com/"+repository)),
		MaxRuns:    maxRuns,
		MaxJobs:    maxJobs,
	}, nil
}
