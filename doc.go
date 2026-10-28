// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

// Package collector is the standalone CI/CD run collector: the fetch plus
// emission halves of the in-tree collector-cicd-run (#8854) on the
// published SDK pair (collector v0.1.0-rc.3, factschema v0.1.0-rc.5).
//
// With no arguments (or --sdk-stdio) the cmd/collector binary serves one
// claim: it reads one collector-sdk/v1alpha1 request from stdin and writes
// one result to stdout. The host owns admission, grants, claim identity,
// fact validation, and process supervision; the handler binds the claim to
// its provider target, resolves the claim credential (static, host-minted
// request token, or a fresh in-satellite App mint), fetches the bounded
// provider window under the claim deadline, and composes the producer
// result. With the seed subcommand it writes its own binary and manifest
// into a directory so an init container can install the component without
// a shell.
//
// Grant issuance, claim control, the commit boundary, durable watermark
// state, and telemetry emission stay core-owned in eshu-hq/eshu; see the
// repository README.
package collector
