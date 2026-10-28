// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"github.com/eshu-hq/eshu-sdk/collector"
)

// CollectorKind is the durable collector family name for CI/CD run facts.
// It matches scope.CollectorCICDRun ("ci_cd_run") in the core.
const CollectorKind = "ci_cd_run"

// ProducerID is the first-party producer identity the core-issued grant
// will bind (see PRODUCER_GRANT_DRAFT in the in-tree PREP). DRAFT: not
// issued, not stored, not enforced.
const ProducerID = "dev.eshu.collector.cicd-run"

// ProducerVersion is the collector artifact version. It is NOT
// a published SDK pin; the SDK pins live in go.mod. 0.1.1 carries the
// #9329 manifest fix (dropped payloadSchemaRef on the three schemaless
// ci kinds); no emitted kind, schema, or scope changed.
const ProducerVersion = "0.1.1"

// SchemaVersion is the first CI/CD run fact schema. Every emitted kind is
// versioned at 1.0.0, matching facts.CICDSchemaVersion in the core.
const SchemaVersion = "1.0.0"

// Provider identifies the CI/CD provider that reported runtime evidence.
type Provider string

const (
	// ProviderGitHubActions identifies GitHub Actions workflow-run evidence.
	ProviderGitHubActions Provider = "github_actions"
	// ProviderGitLabCI identifies GitLab CI/CD pipeline-run evidence.
	ProviderGitLabCI Provider = "gitlab_ci"
)

// Kind constants. The nine emitted kinds match SHIM_MAP section 2 exactly;
// ci.workflow_image_evidence is family-declared but git-collector-owned,
// so this producer must never declare or emit it.
const (
	KindPipelineDefinition     = "ci.pipeline_definition"
	KindRun                    = "ci.run"
	KindJob                    = "ci.job"
	KindStep                   = "ci.step"
	KindArtifact               = "ci.artifact"
	KindTriggerEdge            = "ci.trigger_edge"
	KindEnvironmentObservation = "ci.environment_observation"
	KindDeploymentEvent        = "ci.deployment_event"
	KindWarning                = "ci.warning"
	KindWorkflowImageEvidence  = "ci.workflow_image_evidence"
)

// EmittedKinds returns the nine kinds this producer may declare and emit,
// in SHIM_MAP declaration order.
func EmittedKinds() []string {
	return []string{
		KindPipelineDefinition,
		KindRun,
		KindJob,
		KindStep,
		KindArtifact,
		KindTriggerEdge,
		KindEnvironmentObservation,
		KindDeploymentEvent,
		KindWarning,
	}
}

// ExcludedKinds returns family-declared kinds carved out of this
// producer's emission contract: ci.workflow_image_evidence is emitted by
// the git collector's static workflow-file evidence path, never here.
// A trial emission of an excluded kind fails closed under core admission.
func ExcludedKinds() []string {
	return []string{KindWorkflowImageEvidence}
}

// FamilyKinds returns the full ci_cd_run family census: emitted plus
// excluded. The parity test locks the partition: no kind may slip in or
// out without a deliberate fixture update.
func FamilyKinds() []string {
	return append(EmittedKinds(), ExcludedKinds()...)
}

// SchemaVersionFor returns the schema version for a ci_cd_run family kind.
// The second return is false for kinds outside the family.
func SchemaVersionFor(kind string) (string, bool) {
	for _, family := range FamilyKinds() {
		if family == kind {
			return SchemaVersion, true
		}
	}
	return "", false
}

// Contract returns the collector-sdk/v1alpha1 contract this producer
// emits under: one FactDeclaration per emitted kind, each at schema
// 1.0.0 with reported source confidence. Tombstones are not used by this
// producer.
func Contract() collector.Contract {
	declarations := make([]collector.FactDeclaration, 0, len(EmittedKinds()))
	for _, kind := range EmittedKinds() {
		declarations = append(declarations, collector.FactDeclaration{
			Kind:             kind,
			SchemaVersions:   []string{SchemaVersion},
			SourceConfidence: []collector.SourceConfidence{collector.SourceConfidenceReported},
		})
	}
	return collector.Contract{
		ProtocolVersion: collector.ProtocolVersionV1Alpha1,
		Facts:           declarations,
	}
}
