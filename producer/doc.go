// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

// Package producer is the extracted ci_cd_run fetch-plus-emission half for
// eshu-hq/eshu-collector-cicd-run (#8854, epic #6707; fetch moved in per
// the #9337 arbiter ruling).
//
// It ports the in-tree collector-cicd-run command surface per SHIM_MAP
// (go/cmd/collector-cicd-run/SHIM_MAP.md in eshu-hq/eshu): the targetJSON
// operator shape, provider-scope routing, the ci-cd-run-claim- namespace,
// the nine emitted ci.* kinds at schema 1.0.0, deterministic stable keys,
// URL/text sanitizers, the bounded provider fetch (GitHub Actions plus
// GitLab CI windows, caps, and outcome signals), and the claim-outcome
// table (complete / partial+warning / unchanged / retryable / terminal /
// cancelled).
// Facts use the exact published SDK pair:
//
//	require github.com/eshu-hq/eshu-sdk/collector v0.1.0-rc.3
//	require github.com/eshu-hq/eshu-sdk/factschema v0.1.0-rc.5
//
// with no replace directives and no workspace.
//
// Deliberately NOT ported (core-owned, stays in eshu-hq/eshu):
//
//   - Grant issuance, storage, and revocation (#6709). Authorization is a
//     fail-closed stub (see grant.go): nothing emits until a core-issued
//     grant authorizes it.
//   - Claim control (lease, heartbeat, supervision) and the commit
//     boundary (admission, projection, persistence, fencing). The producer
//     receives a bounded collector.Claim and returns a collector.Result;
//     transitions stay eshu-workflow.
//   - Durable watermark state (migration 078, #5429 gap detection). The
//     external producer keeps no watermark; gap detection across restarts
//     stays core-side until the extracted release proves its own equivalent.
//   - The canonical repository_id derivation
//     (repositoryidentity.CanonicalRepositoryID). Payloads carry the raw
//     provider repository locator as provenance and leave repository_id
//     unset; see emit.go for the open gap.
//
// Telemetry names are preserved verbatim (see telemetry.go); emission of
// those signals stays host-side.
package producer
