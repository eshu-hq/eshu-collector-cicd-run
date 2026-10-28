// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

// ErrScopeNotRegistered marks a claim scope with no registered provider
// target. Callers map it to invalid_config.target_scope_mismatch; the
// message names the scope, never credentials.
var ErrScopeNotRegistered = errors.New("scope has no registered provider source")

// claimIDPrefix namespaces every claim this producer mints. The prefix is
// part of the parity fixture: the #4047 gate reproduces it exactly.
const claimIDPrefix = "ci-cd-run-claim-"

var fallbackClaimSequence uint64

// NewClaimID mints a unique claim identity in the ci-cd-run-claim-
// namespace: crypto-rand hex with a monotonic fallback. It mirrors the
// in-tree service.go newClaimID.
func NewClaimID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return claimIDPrefix + hex.EncodeToString(raw[:])
	}
	next := atomic.AddUint64(&fallbackClaimSequence, 1)
	return fmt.Sprintf("%sfallback-%d-%d", claimIDPrefix, time.Now().UTC().UnixNano(), next)
}

// Router dispatches one claim to the provider source registered for its
// scope_id, so github_actions and gitlab_ci targets can serve claims for
// the SAME ci_cd_run collector instance and claim queue. Routing is by
// scope_id, decided once at config-parse time; an unregistered scope fails
// closed. This mirrors provider_source.go: it is a different dispatch axis
// than the core's multi-source host, which resolves by (collector kind,
// instance id) across collector families.
type Router struct {
	byScopeID map[string]ResolvedTarget
}

// NewRouter builds a Router from the configured targets of both
// providers. At least one target is required: zero configured targets is
// a configuration error the caller should have rejected earlier.
func NewRouter(config Config) (Router, error) {
	byScopeID := make(map[string]ResolvedTarget, len(config.GitHubTargets)+len(config.GitLabTargets))
	for _, target := range config.GitHubTargets {
		byScopeID[target.ScopeID] = target
	}
	for _, target := range config.GitLabTargets {
		byScopeID[target.ScopeID] = target
	}
	if len(byScopeID) == 0 {
		return Router{}, fmt.Errorf("at least one provider-routed ci/cd run target is required")
	}
	return Router{byScopeID: byScopeID}, nil
}

// Resolve returns the target registered for a claim scope_id, or a
// fail-closed error wrapping ErrScopeNotRegistered when no provider
// source owns that scope.
func (r Router) Resolve(scopeID string) (ResolvedTarget, error) {
	target, ok := r.byScopeID[scopeID]
	if !ok {
		return ResolvedTarget{}, fmt.Errorf(
			"ci/cd run scope %q has no registered provider source: %w", scopeID, ErrScopeNotRegistered,
		)
	}
	return target, nil
}
