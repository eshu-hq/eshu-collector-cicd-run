// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Environment keys for the claimed-runtime configuration. Names match the
// in-tree command so operator runbooks transfer unchanged.
const (
	EnvCollectorInstances  = "ESHU_COLLECTOR_INSTANCES_JSON"
	EnvCollectorInstanceID = "ESHU_CICD_RUN_COLLECTOR_INSTANCE_ID"
	EnvPollInterval        = "ESHU_CICD_RUN_POLL_INTERVAL"
	EnvClaimLeaseTTL       = "ESHU_CICD_RUN_CLAIM_LEASE_TTL"
	EnvHeartbeatInterval   = "ESHU_CICD_RUN_HEARTBEAT_INTERVAL"
	EnvOwnerID             = "ESHU_CICD_RUN_COLLECTOR_OWNER_ID"
)

// desiredInstance is the operator-declared collector instance shape this
// producer reads. It mirrors workflow.DesiredCollectorInstance's wire
// fields without importing the core: validation here is intentionally
// limited to the claim boundary (kind, enabled, claims-enabled); deeper
// admission stays core-owned.
type desiredInstance struct {
	InstanceID    string `json:"instance_id"`
	CollectorKind string `json:"collector_kind"`
	Mode          string `json:"mode"`
	Enabled       bool   `json:"enabled"`
	ClaimsEnabled bool   `json:"claims_enabled"`
	// Configuration stays a raw string: deeper configuration admission is
	// core-owned, and unknown fields pass through untouched here.
	Configuration string `json:"configuration"`
}

// targetJSON is one configured CI/CD target. Provider selects which
// runtime the target builds under: "github_actions" (the default when
// omitted, backward compatible per #5427) or "gitlab_ci". Secrets travel
// by env reference, never inline. MaxArtifacts is GitHub-Actions-only and
// silently ignored for gitlab_ci.
type targetJSON struct {
	Provider            string   `json:"provider"`
	ScopeID             string   `json:"scope_id"`
	Repository          string   `json:"repository"`
	TokenEnv            string   `json:"token_env"`
	AllowedRepositories []string `json:"allowed_repositories"`
	APIBaseURL          string   `json:"api_base_url"`
	MaxRuns             int      `json:"max_runs"`
	MaxJobs             int      `json:"max_jobs"`
	MaxArtifacts        int      `json:"max_artifacts"`
	SourceURI           string   `json:"source_uri"`
}

// ResolvedTarget is one validated target with its credential resolved
// from the environment into a runtime-only field. A token is never copied
// into desired collector config output, facts, logs, or metric labels.
type ResolvedTarget struct {
	Provider     Provider
	ScopeID      string
	Repository   string
	Token        string
	Allowed      []string
	APIBaseURL   string
	SourceURI    string
	MaxRuns      int
	MaxJobs      int
	MaxArtifacts int
}

// Config is the claimed-runtime configuration for one ci_cd_run instance:
// its targets split by declared provider. One or the other side may be
// empty when the instance configures a single provider.
type Config struct {
	InstanceID        string
	CollectorKind     string
	OwnerID           string
	PollInterval      time.Duration
	ClaimLeaseTTL     time.Duration
	HeartbeatInterval time.Duration
	GitHubTargets     []ResolvedTarget
	GitLabTargets     []ResolvedTarget
}

// LoadConfig parses ESHU_COLLECTOR_INSTANCES_JSON via getenv, selects the
// single enabled claim-capable ci_cd_run instance, and resolves every
// target's token_env credential from the environment.
func LoadConfig(instancesJSON, instanceID string, getenv func(string) string) (Config, error) {
	return loadConfigWithDurations(instancesJSON, instanceID, getenv, "", "")
}

// LoadConfigWithDurations is LoadConfig with explicit lease/heartbeat
// overrides for the heartbeat-under-lease guard proof. Empty values fall
// back to getenv, then to the core defaults.
func LoadConfigWithDurations(instancesJSON, instanceID string, getenv func(string) string, leaseTTL, heartbeat string) (Config, error) {
	return loadConfigWithDurations(instancesJSON, instanceID, getenv, leaseTTL, heartbeat)
}

func loadConfigWithDurations(instancesJSON, instanceID string, getenv func(string) string, leaseOverride, heartbeatOverride string) (Config, error) {
	instance, err := selectInstance(instancesJSON, instanceID)
	if err != nil {
		return Config{}, err
	}
	if err := validateInstance(instance); err != nil {
		return Config{}, err
	}
	githubTargets, gitlabTargets, err := splitTargets(instance, getenv)
	if err != nil {
		return Config{}, err
	}
	pollInterval, err := envDuration(getenv, EnvPollInterval, time.Second)
	if err != nil {
		return Config{}, err
	}
	// Fallbacks mirror workflow.DefaultClaimLeaseTTL (60s) and
	// workflow.DefaultHeartbeatInterval (20s) in the core; the port
	// duplicates the values rather than the dependency so the extracted
	// module keeps no core import.
	claimLeaseTTL, err := envDuration(getenv, EnvClaimLeaseTTL, 60*time.Second)
	if err != nil {
		return Config{}, err
	}
	heartbeatInterval, err := envDuration(getenv, EnvHeartbeatInterval, 20*time.Second)
	if err != nil {
		return Config{}, err
	}
	if leaseOverride != "" {
		claimLeaseTTL, err = time.ParseDuration(leaseOverride)
		if err != nil {
			return Config{}, fmt.Errorf("parse lease override: %w", err)
		}
	}
	if heartbeatOverride != "" {
		heartbeatInterval, err = time.ParseDuration(heartbeatOverride)
		if err != nil {
			return Config{}, fmt.Errorf("parse heartbeat override: %w", err)
		}
	}
	if heartbeatInterval >= claimLeaseTTL {
		return Config{}, fmt.Errorf("ci/cd run collector heartbeat interval must be less than claim lease TTL")
	}
	return Config{
		InstanceID:        instance.InstanceID,
		CollectorKind:     instance.CollectorKind,
		OwnerID:           ownerID(getenv),
		PollInterval:      pollInterval,
		ClaimLeaseTTL:     claimLeaseTTL,
		HeartbeatInterval: heartbeatInterval,
		GitHubTargets:     githubTargets,
		GitLabTargets:     gitlabTargets,
	}, nil
}

// instanceWire mirrors the operator JSON shape; configuration stays raw
// so unknown fields pass through untouched.
type instanceWire struct {
	InstanceID    string          `json:"instance_id"`
	CollectorKind string          `json:"collector_kind"`
	Mode          string          `json:"mode"`
	Enabled       bool            `json:"enabled"`
	ClaimsEnabled bool            `json:"claims_enabled"`
	Configuration json.RawMessage `json:"configuration"`
}

func selectInstance(instancesJSON, requestedID string) (desiredInstance, error) {
	var wires []instanceWire
	if err := json.Unmarshal([]byte(instancesJSON), &wires); err != nil {
		return desiredInstance{}, fmt.Errorf("parse %s: %w", EnvCollectorInstances, err)
	}
	requestedID = strings.TrimSpace(requestedID)
	var matches []desiredInstance
	for _, wire := range wires {
		if wire.CollectorKind != CollectorKind {
			continue
		}
		if requestedID != "" && wire.InstanceID != requestedID {
			continue
		}
		matches = append(matches, desiredInstance{
			InstanceID:    wire.InstanceID,
			CollectorKind: wire.CollectorKind,
			Mode:          wire.Mode,
			Enabled:       wire.Enabled,
			ClaimsEnabled: wire.ClaimsEnabled,
			Configuration: string(wire.Configuration),
		})
	}
	switch len(matches) {
	case 0:
		if requestedID != "" {
			return desiredInstance{}, fmt.Errorf("ci/cd run collector instance %q not found", requestedID)
		}
		return desiredInstance{}, fmt.Errorf("no ci/cd run collector instance configured")
	case 1:
		return matches[0], nil
	default:
		return desiredInstance{}, fmt.Errorf("multiple ci/cd run collector instances configured; set %s", EnvCollectorInstanceID)
	}
}

// validateInstance enforces the claim boundary: this library only serves
// claim-capable instances. It mirrors validateCICDRunInstance minus the
// core-owned workflow.Validate calls, which stay in the core.
func validateInstance(instance desiredInstance) error {
	if strings.TrimSpace(instance.InstanceID) == "" {
		return fmt.Errorf("ci/cd run collector requires a non-blank instance_id")
	}
	if instance.CollectorKind != CollectorKind {
		return fmt.Errorf("ci/cd run collector requires collector_kind %q", CollectorKind)
	}
	if !instance.Enabled {
		return fmt.Errorf("ci/cd run collector requires enabled collector instance")
	}
	if !instance.ClaimsEnabled {
		return fmt.Errorf("ci/cd run collector requires claim-enabled collector instance")
	}
	if strings.TrimSpace(instance.Configuration) == "" {
		return fmt.Errorf("ci/cd run collector configuration: missing targets")
	}
	return nil
}

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

// mapGitHubTarget resolves one github_actions target. The token resolves
// from the environment at startup into a runtime-only field; the error
// names the env reference, never the value.
func mapGitHubTarget(target targetJSON, getenv func(string) string) (ResolvedTarget, error) {
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
	return ResolvedTarget{
		Provider:     ProviderGitHubActions,
		ScopeID:      strings.TrimSpace(target.ScopeID),
		Repository:   repository,
		Token:        token,
		Allowed:      cleanStrings(target.AllowedRepositories),
		APIBaseURL:   strings.TrimRight(strings.TrimSpace(target.APIBaseURL), "/"),
		SourceURI:    strings.TrimSpace(firstNonBlank(target.SourceURI, "https://github.com/"+repository)),
		MaxRuns:      maxRuns,
		MaxJobs:      target.MaxJobs,
		MaxArtifacts: target.MaxArtifacts,
	}, nil
}

// mapGitLabTarget resolves one gitlab_ci target. MaxArtifacts has no
// GitLab counterpart (job artifacts report inline) and is silently
// ignored rather than rejected.
func mapGitLabTarget(target targetJSON, getenv func(string) string) (ResolvedTarget, error) {
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
	return ResolvedTarget{
		Provider:   ProviderGitLabCI,
		ScopeID:    strings.TrimSpace(target.ScopeID),
		Repository: repository,
		Token:      token,
		Allowed:    cleanStrings(target.AllowedRepositories),
		APIBaseURL: strings.TrimRight(strings.TrimSpace(target.APIBaseURL), "/"),
		SourceURI:  strings.TrimSpace(firstNonBlank(target.SourceURI, "https://gitlab.com/"+repository)),
		MaxRuns:    maxRuns,
		MaxJobs:    target.MaxJobs,
	}, nil
}

func envDuration(getenv func(string) string, key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("%s must be positive", key)
	}
	return value, nil
}

func ownerID(getenv func(string) string) string {
	for _, key := range []string{EnvOwnerID, "HOSTNAME"} {
		if value := strings.TrimSpace(getenv(key)); value != "" {
			return value
		}
	}
	return "collector-cicd-run"
}

// resolveMaxRuns ports the in-tree run-window bound
// (ghactionsruntime/source_target.go): 0 selects the default of 10, values
// above the hard cap of 100 are rejected. The emission half carries the
// bound for the host fetch loop; no fetch loop ships here.
func resolveMaxRuns(raw int) (int, error) {
	const (
		defaultMaxRuns = 10
		maxRunPages    = 100
	)
	if raw == 0 {
		return defaultMaxRuns, nil
	}
	if raw < 0 || raw > maxRunPages {
		return 0, fmt.Errorf("max_runs must be between 0 and %d (0 uses the default of %d)", maxRunPages, defaultMaxRuns)
	}
	return raw, nil
}

func cleanStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
