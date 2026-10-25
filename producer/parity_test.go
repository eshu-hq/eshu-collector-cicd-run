// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

// Package producer parity tests (#8854, extracted side): the exact census
// agreement the #4047 cutover gate must reproduce — the checked-in fixture
// pack drives the ported producer and asserts the emission-half contract
// from SHIM_MAP: the nine emitted ci.* kinds at schema 1.0.0 (never the
// git-collector-owned ci.workflow_image_evidence), deterministic
// generation-agnostic stable keys, fencing carried on claim and generation,
// credential-from-env-only config, heartbeat-under-lease, the
// ci-cd-run-claim- namespace, provider-scope routing, and the SHIM_MAP
// outcome table (complete / partial+warning / unchanged / retryable /
// terminal / cancelled-no-result) with hostile inputs failing closed.
//
// #6710 harness note: grant enforcement is a fail-closed stub here
// (see producer/grant.go and PRODUCER_GRANT_DRAFT in the in-tree PREP).
// Core owns admission, projection, persistence, claim mutation, fencing,
// and the status read; this producer only emits through an explicitly
// authorized test stand-in. Production wiring keeps the DenyAll
// authorizer until core-issued grants land per #6709.
package producer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eshu-hq/eshu-sdk/collector"
)

// parityFixture mirrors testdata/8854-parity-cicd-run.json, copied verbatim
// from go/cmd/collector-cicd-run/testdata in eshu-hq/eshu.
type parityFixture struct {
	Instances []json.RawMessage `json:"instances"`
	Expected  struct {
		CollectorKind string   `json:"collector_kind"`
		ClaimPrefix   string   `json:"claim_id_prefix"`
		GitHubScopes  []string `json:"github_scopes"`
		GitLabScopes  []string `json:"gitlab_scopes"`
		EmittedKinds  []string `json:"emitted_kinds"`
		ExcludedKinds []string `json:"excluded_kinds"`
		Schema        string   `json:"schema_version"`
	} `json:"expected"`
	Tokens map[string]string `json:"tokens"`
}

func loadParityFixture(t *testing.T) parityFixture {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("..", "testdata", "8854-parity-cicd-run.json"))
	if err != nil {
		t.Fatalf("ReadFile(parity fixture) error = %v, want checked-in census fixture", err)
	}
	var fixture parityFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("Unmarshal(parity fixture) error = %v, want valid JSON", err)
	}
	return fixture
}

func fixtureInstancesJSON(t *testing.T, fixture parityFixture) string {
	t.Helper()

	raw, err := json.Marshal(fixture.Instances)
	if err != nil {
		t.Fatalf("Marshal(instances) error = %v", err)
	}
	return string(raw)
}

func fixtureGetenv(fixture parityFixture, instancesJSON string) func(string) string {
	return func(key string) string {
		switch key {
		case EnvCollectorInstances:
			return instancesJSON
		case EnvCollectorInstanceID:
			return "cicd-run-primary"
		default:
			return fixture.Tokens[key]
		}
	}
}

func TestParityFixtureKindCensus(t *testing.T) {
	t.Parallel()

	fixture := loadParityFixture(t)
	if got := EmittedKinds(); !equalStrings(got, fixture.Expected.EmittedKinds) {
		t.Errorf("EmittedKinds() = %v, want fixture %v", got, fixture.Expected.EmittedKinds)
	}
	for _, excluded := range fixture.Expected.ExcludedKinds {
		for _, emitted := range EmittedKinds() {
			if emitted == excluded {
				t.Errorf("excluded kind %q present in emission contract; %s is git-collector-owned", excluded, excluded)
			}
		}
	}
	family := map[string]bool{}
	for _, kind := range FamilyKinds() {
		family[kind] = true
		version, ok := SchemaVersionFor(kind)
		if !ok || version != fixture.Expected.Schema {
			t.Errorf("SchemaVersionFor(%q) = (%q, %v), want (%q, true)", kind, version, ok, fixture.Expected.Schema)
		}
	}
	for _, want := range append(append([]string{}, fixture.Expected.EmittedKinds...), fixture.Expected.ExcludedKinds...) {
		if !family[want] {
			t.Errorf("family missing kind %q from parity fixture", want)
		}
	}
	if got, want := len(family), len(fixture.Expected.EmittedKinds)+len(fixture.Expected.ExcludedKinds); got != want {
		t.Errorf("family kind count = %d, want %d; update deliberately, never silently", got, want)
	}
	contract := Contract()
	if contract.ProtocolVersion != collector.ProtocolVersionV1Alpha1 {
		t.Errorf("contract protocol = %q, want %q", contract.ProtocolVersion, collector.ProtocolVersionV1Alpha1)
	}
	declared := map[string]string{}
	for _, decl := range contract.Facts {
		declared[decl.Kind] = strings.Join(decl.SchemaVersions, ",")
	}
	for _, kind := range fixture.Expected.EmittedKinds {
		versions, ok := declared[kind]
		if !ok {
			t.Errorf("contract missing declaration for emitted kind %q", kind)
			continue
		}
		if versions != fixture.Expected.Schema {
			t.Errorf("contract schema for %q = %q, want %q", kind, versions, fixture.Expected.Schema)
		}
	}
}

func TestParityFixtureConfigAgreement(t *testing.T) {
	t.Parallel()

	fixture := loadParityFixture(t)
	config, err := LoadConfig(fixtureInstancesJSON(t, fixture), "cicd-run-primary", fixtureGetenv(fixture, fixtureInstancesJSON(t, fixture)))
	if err != nil {
		t.Fatalf("LoadConfig(fixture) error = %v, want nil", err)
	}
	if config.CollectorKind != fixture.Expected.CollectorKind {
		t.Errorf("CollectorKind = %q, want %q", config.CollectorKind, fixture.Expected.CollectorKind)
	}
	githubScopes := map[string]bool{}
	for _, target := range config.GitHubTargets {
		githubScopes[target.ScopeID] = true
		if strings.Contains(target.Token, "token-value") && target.Token != "github-token-value" {
			t.Errorf("GitHub target token unexpected value %q", target.Token)
		}
	}
	for _, want := range fixture.Expected.GitHubScopes {
		if !githubScopes[want] {
			t.Errorf("GitHub scopes missing %q (have %+v)", want, config.GitHubTargets)
		}
	}
	gitlabScopes := map[string]bool{}
	for _, target := range config.GitLabTargets {
		gitlabScopes[target.ScopeID] = true
	}
	for _, want := range fixture.Expected.GitLabScopes {
		if !gitlabScopes[want] {
			t.Errorf("GitLab scopes missing %q (have %+v)", want, config.GitLabTargets)
		}
	}
	if got := NewClaimID(); !strings.HasPrefix(got, fixture.Expected.ClaimPrefix) {
		t.Errorf("NewClaimID() = %q, want prefix %q", got, fixture.Expected.ClaimPrefix)
	}
}

func TestParityHostileInputsFailClosed(t *testing.T) {
	t.Parallel()

	fixture := loadParityFixture(t)
	instancesJSON := fixtureInstancesJSON(t, fixture)
	getenv := fixtureGetenv(fixture, instancesJSON)

	// Ambiguous multi-instance without a selector fails closed.
	twoInstances := `[` + instancesJSON[1:len(instancesJSON)-1] + `,{
		"instance_id": "cicd-run-secondary",
		"collector_kind": "ci_cd_run",
		"mode": "continuous",
		"enabled": true,
		"claims_enabled": true,
		"configuration": {"targets": []}
	}]`
	if _, err := LoadConfig(twoInstances, "", getenv); err == nil {
		t.Error("LoadConfig(two instances, no selector) = nil, want ambiguity rejection")
	}

	// Missing token_env credential fails closed without leaking values.
	if _, err := LoadConfig(instancesJSON, "cicd-run-primary", func(string) string { return "" }); err == nil {
		t.Fatal("LoadConfig(missing tokens) = nil, want credential error")
	} else if strings.Contains(err.Error(), "github-token-value") || strings.Contains(err.Error(), "gitlab-token-value") {
		t.Fatalf("LoadConfig() error = %q leaks a credential value", err.Error())
	}

	// Heartbeat at or above the lease fails closed; below passes.
	if _, err := LoadConfigWithDurations(instancesJSON, "cicd-run-primary", getenv, "30s", "30s"); err == nil {
		t.Error("LoadConfig(heartbeat == lease) = nil, want heartbeat-under-lease rejection")
	}
	if _, err := LoadConfigWithDurations(instancesJSON, "cicd-run-primary", getenv, "30s", "10s"); err != nil {
		t.Errorf("LoadConfig(heartbeat < lease) error = %v, want nil", err)
	}

	// An unrecognized provider fails closed at parse time.
	badProvider := `[{
		"instance_id": "cicd-run-primary",
		"collector_kind": "ci_cd_run",
		"mode": "continuous",
		"enabled": true,
		"claims_enabled": true,
		"configuration": {"targets": [{
			"provider": "bitbucket_pipelines",
			"scope_id": "ci-cd:bitbucket:example/repo",
			"repository": "example/repo",
			"token_env": "GITHUB_TOKEN",
			"max_runs": 1
		}]}
	}]`
	if _, err := LoadConfig(badProvider, "cicd-run-primary", getenv); err == nil {
		t.Fatal("LoadConfig(bitbucket) = nil, want unsupported provider rejection")
	} else if !strings.Contains(err.Error(), "unsupported ci/cd run provider") {
		t.Fatalf("LoadConfig() error = %q, want unsupported provider", err.Error())
	}

	// An unregistered scope fails closed at dispatch.
	config, err := LoadConfig(instancesJSON, "cicd-run-primary", getenv)
	if err != nil {
		t.Fatalf("LoadConfig(fixture) error = %v, want nil", err)
	}
	router, err := NewRouter(config)
	if err != nil {
		t.Fatalf("NewRouter() error = %v, want nil", err)
	}
	if _, err := router.Resolve("gitlab-ci://gitlab.com/eshu-hq/unknown"); err == nil {
		t.Error("Resolve(unregistered scope) = nil, want no-registered-source error")
	} else if !strings.Contains(err.Error(), "has no registered provider source") {
		t.Fatalf("Resolve() error = %q, want no-registered-source", err.Error())
	}
	if _, err := NewRouter(Config{}); err == nil {
		t.Error("NewRouter(empty) = nil, want non-nil")
	}
}

func TestParityMaxRunsBound(t *testing.T) {
	t.Parallel()

	for raw, want := range map[int]int{0: 10, 1: 1, 100: 100} {
		if got, err := resolveMaxRuns(raw); err != nil || got != want {
			t.Errorf("resolveMaxRuns(%d) = (%d, %v), want (%d, nil)", raw, got, err, want)
		}
	}
	for _, raw := range []int{-1, 101} {
		if got, err := resolveMaxRuns(raw); err == nil {
			t.Errorf("resolveMaxRuns(%d) = (%d, nil), want range rejection", raw, got)
		}
	}

	getenv := func(key string) string {
		if key == "GITHUB_TOKEN" {
			return "token-value"
		}
		return ""
	}
	zeroRuns := `[{
		"instance_id": "cicd-run-primary",
		"collector_kind": "ci_cd_run",
		"mode": "continuous",
		"enabled": true,
		"claims_enabled": true,
		"configuration": {"targets": [{
			"provider": "github_actions",
			"scope_id": "ci-cd:github-actions:example-org/example-repo",
			"repository": "example-org/example-repo",
			"token_env": "GITHUB_TOKEN",
			"max_runs": 0
		}]}
	}]`
	config, err := LoadConfig(zeroRuns, "cicd-run-primary", getenv)
	if err != nil {
		t.Fatalf("LoadConfig(max_runs 0) error = %v, want nil", err)
	}
	if len(config.GitHubTargets) != 1 || config.GitHubTargets[0].MaxRuns != 10 {
		t.Errorf("max_runs 0 wired MaxRuns = %v, want default 10", config.GitHubTargets)
	}
	overCap := `[{
		"instance_id": "cicd-run-primary",
		"collector_kind": "ci_cd_run",
		"mode": "continuous",
		"enabled": true,
		"claims_enabled": true,
		"configuration": {"targets": [{
			"provider": "github_actions",
			"scope_id": "ci-cd:github-actions:example-org/example-repo",
			"repository": "example-org/example-repo",
			"token_env": "GITHUB_TOKEN",
			"max_runs": 101
		}]}
	}]`
	if _, err := LoadConfig(overCap, "cicd-run-primary", getenv); err == nil {
		t.Error("LoadConfig(max_runs 101) = nil, want hard-cap rejection")
	} else if !strings.Contains(err.Error(), "max_runs") {
		t.Errorf("LoadConfig() error = %q, want max_runs range error", err.Error())
	}
}

func TestParityClaimIDNamespace(t *testing.T) {
	t.Parallel()

	first, second := NewClaimID(), NewClaimID()
	for _, id := range []string{first, second} {
		if !strings.HasPrefix(id, "ci-cd-run-claim-") {
			t.Errorf("claim ID %q missing ci-cd-run-claim- namespace prefix", id)
		}
	}
	if first == second {
		t.Error("consecutive claim IDs identical; claim identity must be unique per acquisition")
	}
}

// testAuthorizer is the test-only stand-in for a core-issued grant: this
// repository ships a library only, so library use must attach an
// authorizer (DenyAllAuthorizer until #6709 issuance lands).
type testAuthorizer struct{}

func testClaim(router Router, config Config, scopeID, generationID string) collector.Claim {
	provider, err := router.Resolve(scopeID)
	if err != nil {
		panic(err)
	}
	_ = provider
	return validSDKClaim(scopeID, generationID)
}

func testClaimForScope(t *testing.T, config Config, scopeID, generationID string) collector.Claim {
	t.Helper()

	router, err := NewRouter(config)
	if err != nil {
		t.Fatalf("NewRouter() error = %v, want nil", err)
	}
	return testClaim(router, config, scopeID, generationID)
}

func validSDKClaim(scopeID, generationID string) collector.Claim {
	return collector.Claim{
		ComponentID:   "dev.eshu.collector.cicd-run",
		InstanceID:    "cicd-run-primary",
		CollectorKind: "ci_cd_run",
		SourceSystem:  "ci_cd_run",
		Scope:         collector.Scope{ID: scopeID, Kind: "ci_cd_run"},
		SourceRunID:   "run-8854",
		GenerationID:  generationID,
		WorkItemID:    "work-8854",
		FencingToken:  "fencing-8854",
		Attempt:       1,
		Deadline:      time.Now().UTC().Add(time.Hour),
		ConfigHandle:  "opaque-config-handle",
	}
}

func loadSingleTargetConfig(t *testing.T) Config {
	t.Helper()

	fixture := loadParityFixture(t)
	instancesJSON := fixtureInstancesJSON(t, fixture)
	config, err := LoadConfig(instancesJSON, "cicd-run-primary", fixtureGetenv(fixture, instancesJSON))
	if err != nil {
		t.Fatalf("LoadConfig(fixture) error = %v, want nil", err)
	}
	return config
}

func singleTargetConfig(t *testing.T) Config {
	t.Helper()

	return loadSingleTargetConfig(t)
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func equalAsSets(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	counts := map[string]int{}
	for _, value := range left {
		counts[value]++
	}
	for _, value := range right {
		counts[value]--
		if counts[value] < 0 {
			return false
		}
	}
	return true
}
