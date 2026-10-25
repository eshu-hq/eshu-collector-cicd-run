// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"testing"

	"github.com/eshu-hq/eshu-sdk/collector"
)

// Golden parity vectors for eshu-hq/eshu#8854.
//
// Every stable key below was cut from the in-tree producer
// (go/internal/collector/cicdrun on the 8854 PREP branch) by feeding its
// real fixture entry points the same logical observation the golden
// evidence builders in this file carry, and dumping kind/stable-key/
// payload. The port must reproduce every key byte-identically, except
// ci.pipeline_definition (see expected-diverge note). Payload spot checks
// cover every field the Evidence seam carries; repository_id must be
// ABSENT from every port payload until the SDK exports
// CanonicalRepositoryID (cutover blocker on #8854).

func goldenGitHubEvidence() Evidence {
	return Evidence{
		Provider:   "github_actions",
		RunID:      "123456",
		RunAttempt: "1",
		RunNumber:  "7",
		Repository: RepositoryEvidence{
			FullName: "example-org/example-repo",
			HTMLURL:  "https://github.com/example-org/example-repo",
		},
		RunName:    "CI",
		Event:      "push",
		Status:     "completed",
		Conclusion: "success",
		Branch:     "main",
		CommitSHA:  "abc123def456",
		RunURL:     "https://github.com/example-org/example-repo/actions/runs/123456",
		StartedAt:  "2026-10-24T00:00:00Z",
		UpdatedAt:  "2026-10-24T00:05:00Z",
		Actor:      "octocat",
		Workflow:   &WorkflowEvidence{ID: "789", Name: "CI", Path: ".github/workflows/ci.yml", State: "active", Trigger: "push"},
		Jobs: []JobEvidence{{
			ID:          "111",
			Name:        "build",
			Status:      "completed",
			Conclusion:  "success",
			StartedAt:   "2026-10-24T00:00:10Z",
			CompletedAt: "2026-10-24T00:04:00Z",
			Labels:      []string{"ubuntu-latest"},
			Environment: "prod",
			Steps: []StepEvidence{{
				Number:     "1",
				Name:       "Run actions/checkout@v4",
				Status:     "completed",
				Conclusion: "success",
				StartedAt:  "2026-10-24T00:00:11Z",
				Completed:  "2026-10-24T00:00:20Z",
			}},
		}},
		Artifacts: []ArtifactEvidence{{
			ID:          "222",
			Name:        "dist",
			Digest:      "sha256:deadbeef",
			SizeBytes:   128,
			DownloadURL: "https://api.github.com/x/download",
			CreatedAt:   "2026-10-24T00:04:00Z",
			ExpiresAt:   "2027-01-22T00:04:00Z",
		}},
		Triggers: []TriggerEvidence{{Kind: "workflow_call", SourceProvider: "github_actions", SourceRunID: "123455"}},
		Deployments: []DeploymentEvidence{{
			ID:                    "333",
			StatusID:              "444",
			Environment:           "prod",
			OriginalEnvironment:   "prod",
			SHA:                   "abc123def456",
			Ref:                   "refs/heads/main",
			Task:                  "deploy",
			State:                 "success",
			Description:           "Deployed",
			CreatedAt:             "2026-10-24T00:05:00Z",
			UpdatedAt:             "2026-10-24T00:06:00Z",
			EnvironmentURL:        "https://example.com/deploy",
			LogURL:                "https://example.com/deploy/log",
			ProductionEnvironment: true,
		}},
		Warnings: []WarningEvidence{{Key: "fixture-warning:0", Reason: "provider_note", Message: "carrier wave nominal"}},
	}
}

func goldenGitLabEvidence() Evidence {
	return Evidence{
		Provider:   "gitlab_ci",
		RunID:      "42",
		RunAttempt: "1",
		RunNumber:  "7",
		Repository: RepositoryEvidence{
			WebURL: "https://gitlab.com/eshu-hq/demo/-/pipelines/42",
		},
		Source:    "push",
		Status:    "success",
		Branch:    "main",
		CommitSHA: "abc123def456",
		StartedAt: "2026-10-24T00:00:00Z",
		UpdatedAt: "2026-10-24T00:05:00Z",
		Actor:     "octocat",
		Jobs: []JobEvidence{{
			ID:          "7",
			Name:        "test",
			Stage:       "test",
			Status:      "success",
			StartedAt:   "2026-10-24T00:00:10Z",
			CompletedAt: "2026-10-24T00:04:00Z",
			InlineArtifacts: []ArtifactEvidence{
				{ID: "7:report.xml", Name: "report.xml", Type: "archive", SizeBytes: 64},
			},
		}},
	}
}

// In-tree golden stable keys, cut from go/internal/collector/cicdrun on the
// 8854 PREP branch (same logical observation as the builders above).
// ci.pipeline_definition is the single expected-diverge: the in-tree key
// folds the canonical repository_id, which the port cannot compute until
// the SDK exports CanonicalRepositoryID.
var (
	goldenGitHubKeys = map[string]string{
		KindRun:                    "439044d2d02007495126a1d37d032d75d7efe3247fb453ff7bf7a9a1d03e225c",
		KindJob:                    "3b055659071ba02f609ab4689e40e8fafd432547b687bed0e680884bb48967a6",
		KindStep:                   "5d84a799185e4f9bea3ce5a23ade0ebe14b8fe563a6b8d59d26d294ece7c513a",
		KindEnvironmentObservation: "05f618d12f977125c6908b1597be67355c9c8a76ed78608354ec7f3ac0a68d1a",
		KindArtifact:               "6064a137f770fa896b0b5248f9cec85e1d0b25fc4457c9d278c7ce1e80823c50",
		KindTriggerEdge:            "5e993e5e82c31af480ec7db1f6b5adbedefa57a0f60bbb6629bdbb963e7c86f9",
		KindWarning:                "ff26207b7331b9a6efee2a05a8758419f58cbb49895475ee95d9c289c255720c",
		KindDeploymentEvent:        "11ae050f127bc1fb863087a036bf862ea235794a387455fcfe2dc2e311314984",
	}
	goldenGitHubPipelineKey = "3c581b05e362018783d054557b704b9abe979e82da693ac7701fec8716cd5093"
	goldenGitLabKeys        = map[string]string{
		KindRun:      "2dde61017da0ca58e348226025a39f73e66ce24e33cc13c1ce67538a23763787",
		KindJob:      "2ae3adc88910ecccafcfeeea6cf1aa6c2b02f7564fde1e15faeb5d4167c18101",
		KindArtifact: "90d911a7cee03dfd0e717c714ba5b9aa008aaa5f1c8a139229068f511fa59472",
		KindWarning:  "64e91c82e06cb0d82a8691e94bd5a5ed9f51c88035ecce9344032fbde99db801",
	}
)

func goldenFactsByKind(t *testing.T, scope string, evidence Evidence) map[string]collector.Fact {
	t.Helper()

	config := loadSingleTargetConfig(t)
	claim := testClaimForScope(t, config, scope, "gen-8854-golden")
	result, err := BuildResult(claim, config, evidence, testAuthorizer{})
	if err != nil {
		t.Fatalf("BuildResult(%s) error = %v, want nil", scope, err)
	}
	byKind := map[string]collector.Fact{}
	for _, fact := range result.Facts {
		if _, dup := byKind[fact.Kind]; dup {
			t.Fatalf("duplicate kind %q in golden emission", fact.Kind)
		}
		byKind[fact.Kind] = fact
		if _, present := fact.Payload["repository_id"]; present {
			t.Errorf("kind %q payload carries repository_id: SDK helper not yet exported, must stay absent", fact.Kind)
		}
	}
	return byKind
}

func TestParityGoldenGitHubKeys(t *testing.T) {
	t.Parallel()

	byKind := goldenFactsByKind(t, "ci-cd:github-actions:example-org/example-repo", goldenGitHubEvidence())
	for kind, want := range goldenGitHubKeys {
		fact, ok := byKind[kind]
		if !ok {
			t.Errorf("golden emission missing kind %q", kind)
			continue
		}
		if fact.StableKey != want {
			t.Errorf("kind %q stable key = %q, want in-tree golden %q", kind, fact.StableKey, want)
		}
	}
	// Expected-diverge: the in-tree pipeline key folds the canonical
	// repository_id; the port must NOT match it (matching would mean a
	// locally minted divergent id). provider_repository_id still matches.
	pipeline := byKind[KindPipelineDefinition]
	if pipeline.StableKey == goldenGitHubPipelineKey {
		t.Errorf("pipeline_definition key matches in-tree: expected-diverge violated (repository_id must stay unset)")
	}
	if got := pipeline.Payload["provider_repository_id"]; got != "github.com/example-org/example-repo" {
		t.Errorf("pipeline provider_repository_id = %v, want github.com/example-org/example-repo", got)
	}
}

func TestParityGoldenGitHubPayloads(t *testing.T) {
	t.Parallel()

	byKind := goldenFactsByKind(t, "ci-cd:github-actions:example-org/example-repo", goldenGitHubEvidence())
	strField := func(kind, field, want string) {
		t.Helper()
		if got := byKind[kind].Payload[field]; got != want {
			t.Errorf("kind %q payload[%q] = %v, want %q", kind, field, got, want)
		}
	}
	strField(KindRun, "run_number", "7")
	strField(KindRun, "workflow_name", "CI")
	strField(KindRun, "status", "completed")
	strField(KindRun, "result", "success")
	strField(KindRun, "branch", "main")
	strField(KindRun, "commit_sha", "abc123def456")
	strField(KindRun, "actor", "octocat")
	strField(KindJob, "job_id", "111")
	strField(KindJob, "job_name", "build")
	strField(KindStep, "step_number", "1")
	strField(KindStep, "action_ref", "actions/checkout@v4")
	strField(KindEnvironmentObservation, "environment", "prod")
	strField(KindArtifact, "artifact_id", "222")
	strField(KindArtifact, "artifact_digest", "sha256:deadbeef")
	strField(KindArtifact, "artifact_type", "generic")
	strField(KindTriggerEdge, "trigger_kind", "workflow_call")
	strField(KindTriggerEdge, "source_run_id", "123455")
	strField(KindWarning, "reason", "provider_note")
	strField(KindWarning, "message", "carrier wave nominal")
	strField(KindDeploymentEvent, "deployment_id", "333")
	strField(KindDeploymentEvent, "status_id", "444")
	strField(KindDeploymentEvent, "environment", "prod")
	strField(KindDeploymentEvent, "original_environment", "prod")
	strField(KindDeploymentEvent, "description", "Deployed")
	strField(KindDeploymentEvent, "task", "deploy")
	strField(KindDeploymentEvent, "state", "success")
	strField(KindDeploymentEvent, "ref", "refs/heads/main")
	if got := byKind[KindArtifact].Payload["size_bytes"]; got != int64(128) {
		t.Errorf("artifact size_bytes = %v (%T), want int64(128)", got, got)
	}
	if got := byKind[KindDeploymentEvent].Payload["production_environment"]; got != true {
		t.Errorf("deployment production_environment = %v, want true", got)
	}
	// The run's correlation anchors omit the repository anchor: the port
	// cannot compute the canonical repository_id, so it emits exactly the
	// commit + run anchors rather than a wrong triple.
	if got := byKind[KindRun].Payload["correlation_anchors"]; !equalStrings(got.([]string), []string{"abc123def456", "123456"}) {
		t.Errorf("run correlation_anchors = %v, want [sha run] without repository anchor", got)
	}
}

func TestParityGoldenGitLabKeys(t *testing.T) {
	t.Parallel()

	byKind := goldenFactsByKind(t, "gitlab-ci://gitlab.com/eshu-hq/demo", goldenGitLabEvidence())
	for kind, want := range goldenGitLabKeys {
		fact, ok := byKind[kind]
		if !ok {
			t.Errorf("golden emission missing kind %q", kind)
			continue
		}
		if fact.StableKey != want {
			t.Errorf("kind %q stable key = %q, want in-tree golden %q", kind, fact.StableKey, want)
		}
	}
}

func TestParityGoldenGitLabPayloads(t *testing.T) {
	t.Parallel()

	byKind := goldenFactsByKind(t, "gitlab-ci://gitlab.com/eshu-hq/demo", goldenGitLabEvidence())
	strField := func(kind, field, want string) {
		t.Helper()
		if got := byKind[kind].Payload[field]; got != want {
			t.Errorf("kind %q payload[%q] = %v, want %q", kind, field, got, want)
		}
	}
	strField(KindRun, "run_number", "7")
	strField(KindRun, "event", "push")
	strField(KindRun, "status", "success")
	strField(KindRun, "result", "success")
	strField(KindRun, "branch", "main")
	strField(KindRun, "actor", "octocat")
	strField(KindRun, "repository_url", "https://gitlab.com/eshu-hq/demo")
	strField(KindRun, "provider_repository_id", "gitlab.com/eshu-hq/demo")
	strField(KindJob, "job_id", "7")
	strField(KindJob, "stage", "test")
	strField(KindArtifact, "artifact_id", "7:report.xml")
	strField(KindArtifact, "artifact_type", "archive")
	strField(KindArtifact, "artifact_digest", "")
	strField(KindWarning, "reason", "artifact_missing_digest")
	if got := byKind[KindArtifact].Payload["size_bytes"]; got != int64(64) {
		t.Errorf("artifact size_bytes = %v (%T), want int64(64)", got, got)
	}
}
