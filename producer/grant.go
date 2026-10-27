// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"errors"
	"fmt"

	"github.com/eshu-hq/eshu-sdk/collector"
)

// errMissingFencing rejects a claim that carries no fencing token. The
// workflow control store, not the producer, decides lease expiry and
// supersession; the producer must still treat a claim it cannot bind to a
// live fenced generation as foreign and never write for it.
var errMissingFencing = errors.New("claim fencing token is required")

// errGrantDenied is the fail-closed stub denial. It stands in for the
// core-issued grant check (#6709) at install, activation, and every
// emission until grant issuance lands.
var errGrantDenied = errors.New("grant denied: no live core-issued grant for this producer, kind, and scope")

// Authorizer decides whether an emission may proceed for a claim. Process
// mode (cmd/collector) relies on host grant admission and attaches the
// root package's host-admitted authorizer; library use must attach an
// authorizer (DenyAllAuthorizer until core-issued grants land per #6709).
// A nil authorizer denies every emission. See PRODUCER_GRANT_DRAFT in the
// in-tree PREP for the intended one-grant-per-kind issuance shape.
type Authorizer interface {
	Authorize(claim collector.Claim) error
}

// DenyAllAuthorizer is the fail-closed grant stub: every emission is
// denied at install, activation, and per-emission time. Registry read
// failures deny rather than emitting under unknown authorization, and a
// revoked grant fails the next emission terminal — both fall out of
// deny-by-default without extra states.
type DenyAllAuthorizer struct{}

// Authorize implements Authorizer: it always denies.
func (DenyAllAuthorizer) Authorize(collector.Claim) error {
	return errGrantDenied
}

// authorizeClaim enforces the producer-side claim binding that stays
// local even after grants land: the collector kind, instance, scope, and
// generation must all bind to this producer's configuration, and the
// fencing token must be present. A claim that fails any check is foreign
// and never emits.
func authorizeClaimBinding(claim collector.Claim, config Config) (ResolvedTarget, error) {
	if claim.CollectorKind != CollectorKind || claim.CollectorKind != config.CollectorKind {
		return ResolvedTarget{}, fmt.Errorf("claim collector kind %q does not bind this %q producer", claim.CollectorKind, CollectorKind)
	}
	if claim.InstanceID != config.InstanceID {
		return ResolvedTarget{}, fmt.Errorf("claim instance %q does not bind collector instance %q", claim.InstanceID, config.InstanceID)
	}
	if claim.GenerationID == "" {
		return ResolvedTarget{}, fmt.Errorf("claim generation is required")
	}
	if claim.FencingToken == "" {
		return ResolvedTarget{}, errMissingFencing
	}
	router, err := NewRouter(config)
	if err != nil {
		return ResolvedTarget{}, err
	}
	return router.Resolve(claim.Scope.ID)
}
