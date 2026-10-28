// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"strings"
	"testing"
	"time"
)

// configTestGetenv returns a stub getenv backed by values: unset keys
// read blank, so every test pins exactly the environment it needs.
func configTestGetenv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

// configTestInstances wraps one raw configuration document in a single
// enabled claim-capable ci_cd_run instance envelope.
func configTestInstances(configuration string) string {
	return `[{"instance_id":"test-primary","collector_kind":"ci_cd_run",` +
		`"mode":"claimed","enabled":true,"claims_enabled":true,` +
		`"configuration":` + quoteJSONString(configuration) + `}]`
}

func quoteJSONString(raw string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range raw {
		switch r {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		default:
			out.WriteRune(r)
		}
	}
	out.WriteByte('"')
	return out.String()
}

const configTestTarget = `{"targets":[{"provider":"github_actions",` +
	`"scope_id":"ci-cd:github-actions:example-org/example-repo",` +
	`"repository":"example-org/example-repo","token_env":"TEST_TOKEN_CICD"}]}`

func configTestTokenEnv() map[string]string {
	return map[string]string{"TEST_TOKEN_CICD": "secret"}
}

func TestLoadConfigRejectsMalformedInstancesJSON(t *testing.T) {
	t.Parallel()

	if _, err := LoadConfig("{{{", "", configTestGetenv(nil)); err == nil {
		t.Error("LoadConfig(malformed JSON) = nil, want a parse failure")
	}
}

func TestLoadConfigRejectsDisabledInstance(t *testing.T) {
	t.Parallel()

	instances := `[{"instance_id":"test-primary","collector_kind":"ci_cd_run",` +
		`"mode":"claimed","enabled":false,"claims_enabled":true,` +
		`"configuration":"{\"targets\": []}"}]`
	if _, err := LoadConfig(instances, "", configTestGetenv(nil)); err == nil {
		t.Error("LoadConfig(disabled instance) = nil, want the claim-boundary rejection")
	}
}

func TestLoadConfigRejectsBadDurations(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		env  map[string]string
	}{
		{"poll interval", map[string]string{"TEST_TOKEN_CICD": "secret", EnvPollInterval: "bogus"}},
		{"lease TTL", map[string]string{"TEST_TOKEN_CICD": "secret", EnvClaimLeaseTTL: "bogus"}},
		{"heartbeat", map[string]string{"TEST_TOKEN_CICD": "secret", EnvHeartbeatInterval: "bogus"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if _, err := LoadConfig(configTestInstances(configTestTarget), "", configTestGetenv(test.env)); err == nil {
				t.Errorf("LoadConfig(bad %s) = nil, want a duration parse failure", test.name)
			}
		})
	}
}

func TestLoadConfigWithDurationsRejectsBadOverrides(t *testing.T) {
	t.Parallel()

	instances := configTestInstances(configTestTarget)
	getenv := configTestGetenv(configTestTokenEnv())
	if _, err := LoadConfigWithDurations(instances, "", getenv, "bogus", ""); err == nil {
		t.Error("LoadConfigWithDurations(bad lease override) = nil, want a parse failure")
	}
	if _, err := LoadConfigWithDurations(instances, "", getenv, "", "bogus"); err == nil {
		t.Error("LoadConfigWithDurations(bad heartbeat override) = nil, want a parse failure")
	}
}

func TestSelectInstanceMatrix(t *testing.T) {
	t.Parallel()

	two := `[{"instance_id":"a","collector_kind":"ci_cd_run","mode":"claimed",` +
		`"enabled":true,"claims_enabled":true,"configuration":"{}"},` +
		`{"instance_id":"b","collector_kind":"ci_cd_run","mode":"claimed",` +
		`"enabled":true,"claims_enabled":true,"configuration":"{}"}]`
	if _, err := selectInstance(two, "missing"); err == nil {
		t.Error("selectInstance(unknown id) = nil, want not-found")
	}
	if _, err := selectInstance(`[]`, ""); err == nil {
		t.Error("selectInstance(no instances) = nil, want the empty-configuration failure")
	}
	if _, err := selectInstance(two, ""); err == nil {
		t.Error("selectInstance(ambiguous) = nil, want the instance-ID disambiguation failure")
	}
}

func TestValidateInstanceMatrix(t *testing.T) {
	t.Parallel()

	valid := desiredInstance{
		InstanceID: "test-primary", CollectorKind: CollectorKind,
		Mode: "claimed", Enabled: true, ClaimsEnabled: true,
		Configuration: `{"targets": []}`,
	}
	for _, test := range []struct {
		name   string
		mutate func(*desiredInstance)
	}{
		{"blank id", func(d *desiredInstance) { d.InstanceID = "  " }},
		{"wrong kind", func(d *desiredInstance) { d.CollectorKind = "other" }},
		{"disabled", func(d *desiredInstance) { d.Enabled = false }},
		{"claims disabled", func(d *desiredInstance) { d.ClaimsEnabled = false }},
		{"blank configuration", func(d *desiredInstance) { d.Configuration = "  " }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			candidate := valid
			test.mutate(&candidate)
			if err := validateInstance(candidate); err == nil {
				t.Errorf("validateInstance(%s) = nil, want the claim-boundary rejection", test.name)
			}
		})
	}
	if err := validateInstance(valid); err != nil {
		t.Errorf("validateInstance(valid) = %v, want nil", err)
	}
}

func TestEnvDurationMatrix(t *testing.T) {
	t.Parallel()

	if got, err := envDuration(configTestGetenv(nil), EnvPollInterval, time.Second); err != nil || got != time.Second {
		t.Errorf("envDuration(unset) = (%v, %v), want the fallback", got, err)
	}
	if got, err := envDuration(configTestGetenv(map[string]string{EnvPollInterval: "5s"}), EnvPollInterval, time.Second); err != nil || got != 5*time.Second {
		t.Errorf("envDuration(5s) = (%v, %v), want 5s", got, err)
	}
	if _, err := envDuration(configTestGetenv(map[string]string{EnvPollInterval: "bogus"}), EnvPollInterval, time.Second); err == nil {
		t.Error("envDuration(bogus) = nil, want a parse failure")
	}
	for _, raw := range []string{"0s", "-5s"} {
		if _, err := envDuration(configTestGetenv(map[string]string{EnvPollInterval: raw}), EnvPollInterval, time.Second); err == nil {
			t.Errorf("envDuration(%s) = nil, want the positive-duration rejection", raw)
		}
	}
}

func TestOwnerIDMatrix(t *testing.T) {
	t.Parallel()

	if got := ownerID(configTestGetenv(map[string]string{EnvOwnerID: "owner-1"})); got != "owner-1" {
		t.Errorf("ownerID(owner set) = %q, want the configured owner", got)
	}
	if got := ownerID(configTestGetenv(map[string]string{"HOSTNAME": "host-1"})); got != "host-1" {
		t.Errorf("ownerID(hostname fallback) = %q, want the hostname", got)
	}
	if got := ownerID(configTestGetenv(nil)); got != "collector-cicd-run" {
		t.Errorf("ownerID(unset) = %q, want the built-in default", got)
	}
}

func TestFirstNonBlankAllBlank(t *testing.T) {
	t.Parallel()

	if got := firstNonBlank("  ", ""); got != "" {
		t.Errorf("firstNonBlank(blanks) = %q, want empty", got)
	}
}

func TestSplitTargetsRejectsBadConfiguration(t *testing.T) {
	t.Parallel()

	_, _, err := splitTargets(desiredInstance{Configuration: "{invalid"}, configTestGetenv(nil))
	if err == nil {
		t.Error("splitTargets(malformed configuration) = nil, want a decode failure")
	}
}

func TestSplitTargetsWrapsGitLabTargetError(t *testing.T) {
	t.Parallel()

	configuration := `{"targets":[{"provider":"gitlab_ci","scope_id":"s",` +
		`"repository":"o/r","token_env":"T","auth_mode":"host_minted"}]}`
	_, _, err := splitTargets(desiredInstance{Configuration: configuration},
		configTestGetenv(map[string]string{"T": "secret"}))
	if err == nil {
		t.Error("splitTargets(gitlab host_minted) = nil, want the fail-closed auth-mode rejection")
	} else if !strings.Contains(err.Error(), "targets[0]") {
		t.Errorf("splitTargets error = %v, want the target index for operator triage", err)
	}
}

func TestMapGitLabTargetMatrix(t *testing.T) {
	t.Parallel()

	base := targetJSON{Provider: "gitlab_ci", ScopeID: "s", Repository: "o/r", TokenEnv: "T"}
	if _, err := mapGitLabTarget(base, configTestGetenv(nil)); err == nil {
		t.Error("mapGitLabTarget(unresolved token) = nil, want a credential failure")
	}
	badRuns := base
	badRuns.MaxRuns = 101
	if _, err := mapGitLabTarget(badRuns, configTestGetenv(map[string]string{"T": "secret"})); err == nil {
		t.Error("mapGitLabTarget(max_runs over cap) = nil, want the window-bound rejection")
	}
	badJobs := base
	badJobs.MaxJobs = 501
	if _, err := mapGitLabTarget(badJobs, configTestGetenv(map[string]string{"T": "secret"})); err == nil {
		t.Error("mapGitLabTarget(max_jobs over cap) = nil, want the page-bound rejection")
	}
}

func TestResolvedGitHubTargetValidatesShape(t *testing.T) {
	t.Parallel()

	if _, err := resolvedGitHubTarget(targetJSON{}, AuthModeTokenEnv, "", "", "", ""); err == nil {
		t.Error("resolvedGitHubTarget(token_env without token) = nil, want the shape rejection")
	}
}

func TestValidateGitHubAuthRequiresInstallationID(t *testing.T) {
	t.Parallel()

	if err := ValidateGitHubAuth(AuthModeGitHubApp, "", "app-1", "  ", "key"); err == nil {
		t.Error("ValidateGitHubAuth(github_app without installation id) = nil, want rejection")
	}
}

func TestResolveAppCredentialRefRequiresEnvName(t *testing.T) {
	t.Parallel()

	if _, err := resolveAppCredentialRef(configTestGetenv(nil), "github_app_id_env", "   "); err == nil {
		t.Error("resolveAppCredentialRef(blank env name) = nil, want rejection")
	}
}

func TestSchemaVersionForUnknownKind(t *testing.T) {
	t.Parallel()

	if version, ok := SchemaVersionFor("ci.unknown"); ok || version != "" {
		t.Errorf("SchemaVersionFor(unknown) = (%q, %v), want (\"\", false)", version, ok)
	}
}
