// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/eshu-hq/eshu-collector-cicd-run/producer"
	sdk "github.com/eshu-hq/eshu-sdk/collector"
)

// hostAdmittedAuthorizer authorizes every claim the host launches this
// process for. Process mode relies on host grant admission: the extension
// host validates the component manifest against live core-issued grants
// at activation and per emission, and the satellite process only runs
// when the host starts it for an admitted claim. Claim binding (kind,
// instance, scope, generation, fencing) is still enforced per emission
// by the producer, so a claim that is not this producer's never emits.
type hostAdmittedAuthorizer struct{}

// Authorize implements producer.Authorizer: admission already happened
// host-side.
func (hostAdmittedAuthorizer) Authorize(sdk.Claim) error {
	return nil
}

// Handler runs one claim. The zero value is usable: it reads the process
// environment. Set Getenv in tests to inject the activation.
type Handler struct {
	// Getenv resolves the activation environment (the
	// ESHU_COLLECTOR_INSTANCES_JSON operator shape plus the credential
	// env references its targets name); nil falls back to os.Getenv,
	// which is what the binary in cmd/collector injects.
	Getenv func(string) string
	// Log receives one bounded diagnostic line per failed claim; nil
	// discards it. The binary points it at stderr, which the extension
	// host's process runner does not surface (it keeps only a byte
	// count), so the line helps a local run and a test, not an operator
	// in production.
	Log io.Writer
	// HTTPClient overrides the provider and mint transports (nil means
	// bounded default clients). It exists as the fake-server test seam;
	// production leaves it nil.
	HTTPClient *http.Client
}

// Handle maps one host request to the SDK result the host validates and
// commits. It loads the activation, binds the claim to its provider
// target, resolves the claim credential (static token, per-claim host
// token, or a fresh in-satellite App mint), fetches the bounded provider
// window under the claim deadline, and composes one generation. It never
// returns facts together with a failure state and echoes the claim
// unchanged so the host's identity check passes. A claim the satellite
// cannot serve terminal-fails with an invalid_config.<reason> class
// naming the wiring fault; a transient fault releases the claim
// retryable. A cancelled claim returns a non-nil error so the command
// writes no result. Errors and log lines never quote input, config
// values, or credentials.
func (h Handler) Handle(ctx context.Context, request sdk.Request) (sdk.Result, error) {
	claim := request.Claim
	config, err := producer.LoadConfig(
		h.getenv(producer.EnvCollectorInstances),
		h.getenv(producer.EnvCollectorInstanceID),
		h.getenv,
	)
	if err != nil {
		h.logFailure(claim, err)
		return terminalResult(claim, producer.InvalidConfigClass(producer.ReasonActivation)), nil
	}
	target, err := producer.ResolveClaimTarget(claim, config)
	if err != nil {
		h.logFailure(claim, err)
		return terminalResult(claim, bindFailureClass(err)), nil
	}
	token, err := h.claimToken(ctx, target, request.Config)
	if err != nil {
		// A throttled mint releases retryable like any other
		// transient; a denied mint fails with its distinct terminal.
		h.logFailure(claim, err)
		return resultForFetchError(claim, err), nil
	}
	window, err := h.fetchWindow(ctx, claim, target, token)
	if err != nil {
		h.logFailure(claim, err)
		return resultForFetchError(claim, err), nil
	}
	result, err := producer.BuildWindowResult(claim, config, window, hostAdmittedAuthorizer{})
	if err != nil {
		h.logFailure(claim, err)
		return terminalResult(claim, bindFailureClass(err)), nil
	}
	if _, err := sdk.NewValidator(Contract()).ValidateResult(result); err != nil {
		// The producer built a result the pinned SDK rejects: that is a
		// satellite bug, not a claim decision. Fail the command so
		// nothing commits; the detail stays in the error, never in a
		// result the host could persist.
		return sdk.Result{}, errors.New("validate emission result")
	}
	return result, nil
}

// claimToken resolves the Bearer [REDACTED] spends for one claim: the
// static token for token_env, the per-claim host object for host_minted,
// or a fresh in-satellite mint for direct github_app. A failed mint is a
// distinct terminal (never an empty credential), so a mint fault can
// never serve zero facts as success.
func (h Handler) claimToken(ctx context.Context, target producer.ResolvedTarget, config map[string]any) (string, error) {
	switch producer.NormalizeAuthMode(target.AuthMode) {
	case producer.AuthModeTokenEnv:
		if strings.TrimSpace(target.Token) == "" {
			return "", &producer.TerminalError{Reason: producer.ReasonActivation, Cause: errors.New("static token did not resolve a credential")}
		}
		return target.Token, nil
	case producer.AuthModeHostMinted:
		return producer.ResolveHostToken(config, target.ScopeID)
	case producer.AuthModeGitHubApp:
		return producer.MintClaimToken(ctx, target, h.HTTPClient)
	default:
		return "", &producer.TerminalError{Reason: producer.ReasonActivation, Cause: fmt.Errorf("unsupported auth_mode %q", target.AuthMode)}
	}
}

// fetchWindow fetches one claim's bounded provider window under the claim
// deadline: the deadline is the fetch bound, so an overrun release reads
// as transient, never as committed truth.
func (h Handler) fetchWindow(ctx context.Context, claim sdk.Claim, target producer.ResolvedTarget, token string) (producer.Window, error) {
	fetchCtx := ctx
	if !claim.Deadline.IsZero() {
		var cancel context.CancelFunc
		fetchCtx, cancel = context.WithDeadline(ctx, claim.Deadline)
		defer cancel()
	}
	switch target.Provider {
	case producer.ProviderGitHubActions:
		return (producer.GitHubClient{HTTPClient: h.HTTPClient}).FetchWindow(fetchCtx, target, token)
	case producer.ProviderGitLabCI:
		return (producer.GitLabClient{HTTPClient: h.HTTPClient}).FetchWindow(fetchCtx, target, token)
	default:
		return producer.Window{}, &producer.TerminalError{Reason: producer.ReasonActivation, Cause: fmt.Errorf("unsupported provider %q", target.Provider)}
	}
}

// terminalResultFor maps a credential-resolution failure onto its
// terminal: classified terminals keep their invalid_config reason, and
// anything unclassified fails closed as activation.
func terminalResultFor(err error, claim sdk.Claim) sdk.Result {
	var terminal *producer.TerminalError
	if errors.As(err, &terminal) {
		return terminalResult(claim, terminal.FailureClass())
	}
	return terminalResult(claim, producer.InvalidConfigClass(producer.ReasonActivation))
}

// resultForFetchError maps a fetch failure onto its result: transient
// faults release the claim retryable with bounded provider guidance, and
// classified terminals fail with their invalid_config reason. A bare
// rate-limit escape (the clients convert every observed throttle to a
// partial commit or a retryable) releases retryable rather than dropping
// the claim.
func resultForFetchError(claim sdk.Claim, err error) sdk.Result {
	var retryable *producer.RetryableError
	if errors.As(err, &retryable) {
		return retryableResult(claim, retryAfterSeconds(retryable.RetryAfter))
	}
	var limited producer.RateLimitError
	if errors.As(err, &limited) {
		return retryableResult(claim, retryAfterSeconds(limited.RetryAfter))
	}
	return terminalResultFor(err, claim)
}

// bindFailureClass maps a claim-binding failure onto its terminal class:
// an unregistered scope is target_scope_mismatch, every other mismatch
// (kind, instance, generation, fencing) is claim_binding.
func bindFailureClass(err error) string {
	if errors.Is(err, producer.ErrScopeNotRegistered) {
		return producer.InvalidConfigClass(producer.ReasonTargetScopeMismatch)
	}
	return producer.InvalidConfigClass(producer.ReasonClaimBinding)
}

// retryAfterSeconds clamps provider retry guidance onto the result's
// backoff: absent guidance keeps the established 30-second default, and
// guidance past five minutes clamps rather than hiding the work item.
func retryAfterSeconds(retryAfter time.Duration) int {
	seconds := int(retryAfter / time.Second)
	if seconds < 1 {
		return 30
	}
	if seconds > 300 {
		return 300
	}
	return seconds
}

// retryableResult releases a claim for bounded retry. It carries no facts.
func retryableResult(claim sdk.Claim, retryAfterSeconds int) sdk.Result {
	return sdk.Result{
		ProtocolVersion: sdk.ProtocolVersionV1Alpha1,
		State:           sdk.ResultRetryable,
		Claim:           claim,
		Generation:      sdk.Generation{ID: claim.GenerationID, ObservedAt: time.Now().UTC()},
		Statuses: []sdk.Status{{
			Class:             sdk.StatusFailure,
			FailureClass:      "transient_transport",
			RetryAfterSeconds: retryAfterSeconds,
		}},
	}
}

// terminalResult terminal-fails a claim the satellite cannot serve. It
// carries no facts and echoes the claim and generation the host expects.
func terminalResult(claim sdk.Claim, failureClass string) sdk.Result {
	return sdk.Result{
		ProtocolVersion: sdk.ProtocolVersionV1Alpha1,
		State:           sdk.ResultTerminal,
		Claim:           claim,
		Generation:      sdk.Generation{ID: claim.GenerationID, ObservedAt: time.Now().UTC()},
		Statuses: []sdk.Status{{
			Class:        sdk.StatusFailure,
			FailureClass: failureClass,
		}},
	}
}

// logFailure writes one bounded diagnostic line naming the claim and the
// failure. The error texts this handler logs name config keys, claim
// identity, and failure classes only — never credential or evidence
// values.
func (h Handler) logFailure(claim sdk.Claim, err error) {
	if h.Log == nil {
		return
	}
	line, marshalErr := json.Marshal(map[string]string{
		"claim":  claim.WorkItemID,
		"scope":  claim.Scope.ID,
		"error":  err.Error(),
		"result": string(sdk.ResultTerminal),
	})
	if marshalErr != nil {
		return
	}
	_, _ = h.Log.Write(append(line, '\n'))
}

func (h Handler) getenv(key string) string {
	if h.Getenv != nil {
		return h.Getenv(key)
	}
	return os.Getenv(key)
}
