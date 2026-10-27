// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

// Command collector is the Eshu CI/CD run collector entrypoint. With no
// arguments (or --sdk-stdio) it serves one claim: it reads one
// collector-sdk/v1alpha1 request from stdin and writes one result to
// stdout. With the seed subcommand it writes its own binary and manifest
// into a directory so an init container can install the component without
// a shell.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	collector "github.com/eshu-hq/eshu-collector-cicd-run"
	sdk "github.com/eshu-hq/eshu-sdk/collector"
	"github.com/eshu-hq/eshu-sdk/collector/command"
)

const (
	// maxRequestBytes caps the stdin request document. The host sends one
	// claim plus the activation config and the host-supplied normalized
	// evidence for the claim, which can span a full runs window: keep
	// this in step with the host's process.stdout_limit_bytes.
	maxRequestBytes = 64 << 20
	// maxResultBytes caps the encoded result including its newline. A
	// windowed run observation can fill it: keep this in step with the
	// host's config.limits.maxResultBytes. The handler fails terminal
	// rather than let the host drop an oversized result.
	maxResultBytes = 64 << 20
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv); err != nil {
		fmt.Fprintf(os.Stderr, "collector: %v\n", err)
		os.Exit(1)
	}
}

// run dispatches on the first argument: "seed" installs the binary and
// manifest, anything else serves one claim.
func run(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	getenv func(string) string,
) error {
	if len(args) > 0 && args[0] == "seed" {
		return runSeed(args[1:], stdout, getenv)
	}
	return runClaim(ctx, args, stdin, stdout, stderr, getenv)
}

func runClaim(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	getenv func(string) string,
) error {
	flags := flag.NewFlagSet("collector", flag.ContinueOnError)
	flags.SetOutput(stderr)
	// --sdk-stdio is the documented process-adapter argument. Serving one
	// claim from stdin is the only claim mode, so the flag only has to parse.
	flags.Bool("sdk-stdio", true, "read one collector SDK host request from stdin")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	handler := collector.Handler{Getenv: getenv, Log: stderr}
	return command.Run(ctx, stdin, stdout, handler.Handle, command.Options{
		SupportedProtocols: []string{sdk.ProtocolVersionV1Alpha1},
		MaxRequestBytes:    maxRequestBytes,
		MaxResultBytes:     maxResultBytes,
	})
}
