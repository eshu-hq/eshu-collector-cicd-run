// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"github.com/eshu-hq/eshu-collector-cicd-run/producer"
	sdk "github.com/eshu-hq/eshu-sdk/collector"
)

const (
	// ComponentID is the manifest identity of this collector. Keep it in
	// sync with metadata.id in manifest.yaml; it aliases the producer
	// identity the core-issued grant will bind so the two can never drift.
	ComponentID = producer.ProducerID
	// CollectorKind is the durable collector family name for CI/CD run
	// facts. Claims for any other kind are foreign and never emit.
	CollectorKind = producer.CollectorKind
	// SourceSystem is the source system name facts carry. It mirrors the
	// in-tree envelope, which records the collector kind.
	SourceSystem = producer.CollectorKind
	// MetricsPrefix is the component-owned metric prefix the manifest
	// declares. Nothing in this repository emits a metric under it yet;
	// emission of the eshu_dp_ci_cd_run_* signals stays host-side (see
	// producer/telemetry.go).
	MetricsPrefix = "eshu_dp_cicd_run_collector_"
)

// FactKinds returns every fact kind this collector may emit, in the order
// the manifest declares them. ci.workflow_image_evidence is
// family-declared but git-collector-owned and is never declared here.
func FactKinds() []string {
	return producer.EmittedKinds()
}

// Contract returns the SDK fact families this collector may emit: one
// FactDeclaration per emitted kind, each at schema 1.0.0 with reported
// source confidence.
func Contract() sdk.Contract {
	return producer.Contract()
}
