// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/eshu-hq/eshu-collector-cicd-run/producer"
	sdk "github.com/eshu-hq/eshu-sdk/collector"
)

const (
	// EvidenceConfigKey is the request-config key carrying the
	// host-supplied normalized provider observation for the claim. Live
	// provider fetch stays host-side (the satellite ships the emission
	// half only), so the host fetch loop injects one producer.Evidence
	// document per claim here — the same per-claim injection path the
	// host already uses for minted tokens. A request without decodable
	// evidence is a contract violation and terminal-fails the claim.
	EvidenceConfigKey = "evidence"

	// failureInvalidConfig marks a claim the satellite cannot serve: an
	// unloadable activation, missing or malformed host evidence, or a
	// claim that does not bind this producer. All three are deterministic
	// wiring errors a retry cannot fix.
	failureInvalidConfig = "invalid_config"
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
	// env references its targets name); nil selects os.Getenv at the
	// call site in cmd/collector.
	Getenv func(string) string
	// Log receives one bounded diagnostic line per failed claim; nil
	// discards it. The binary points it at stderr, which the extension
	// host's process runner does not surface (it keeps only a byte
	// count), so the line helps a local run and a test, not an operator
	// in production.
	Log io.Writer
}

// Handle maps one host request to the SDK result the host validates and
// commits. It never returns facts together with a failure state and
// echoes the claim unchanged so the host's identity check passes. A
// cancelled claim returns a non-nil error so the command writes no
// result; every other serving failure is a terminal result with no
// facts. Errors and log lines never quote input, config values, or
// credentials.
func (h Handler) Handle(_ context.Context, request sdk.Request) (sdk.Result, error) {
	claim := request.Claim
	config, err := producer.LoadConfig(
		h.getenv(producer.EnvCollectorInstances),
		h.getenv(producer.EnvCollectorInstanceID),
		h.getenv,
	)
	if err != nil {
		h.logFailure(claim, err)
		return terminalResult(claim, failureInvalidConfig), nil
	}
	evidence, err := decodeEvidence(request.Config)
	if err != nil {
		h.logFailure(claim, err)
		return terminalResult(claim, failureInvalidConfig), nil
	}
	result, err := producer.BuildResult(claim, config, evidence, hostAdmittedAuthorizer{})
	if err != nil {
		if isCancellation(err) {
			return sdk.Result{}, err
		}
		h.logFailure(claim, err)
		return terminalResult(claim, failureInvalidConfig), nil
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

// decodeEvidence extracts the host-supplied normalized observation from
// the request config. The value crossed a JSON boundary on stdin, so it
// round-trips through JSON into producer.Evidence; anything missing or
// misshapen is a contract violation.
func decodeEvidence(config map[string]any) (producer.Evidence, error) {
	raw, ok := config[EvidenceConfigKey]
	if !ok || raw == nil {
		return producer.Evidence{}, errors.New("request config carries no claim evidence")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return producer.Evidence{}, errors.New("request claim evidence is not JSON")
	}
	var evidence producer.Evidence
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&evidence); err != nil {
		return producer.Evidence{}, fmt.Errorf("decode claim evidence: %s", decodeReason(err))
	}
	return evidence, nil
}

// decodeReason names a JSON failure class without quoting the input.
func decodeReason(err error) string {
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		return "malformed JSON"
	}
	var unmarshalType *json.UnmarshalTypeError
	if errors.As(err, &unmarshalType) {
		return "unexpected JSON type"
	}
	return "invalid JSON"
}

// isCancellation reports the producer's no-result cancellation without
// matching its message: any error naming cancellation ends the command
// with no result.
func isCancellation(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "cancel")
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
	return ""
}
