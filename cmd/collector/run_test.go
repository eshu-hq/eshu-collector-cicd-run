// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunDispatchesSeedSubcommand(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"seed", "--bad-flag"},
		strings.NewReader(""), &stdout, &stderr, func(string) string { return "" })
	if err == nil {
		t.Error("run(seed --bad-flag) = nil, want the seed flag failure through the dispatch")
	}
}

func TestRunClaimRejectsUnknownFlag(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"--no-such-flag"},
		strings.NewReader(""), &stdout, &stderr, func(string) string { return "" })
	if err == nil {
		t.Error("run(--no-such-flag) = nil, want the claim flag parse failure")
	}
}

func TestRunSeedFailsWhenDestIsAFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	writeFile(t, blocker, "x", 0o600)
	var stdout bytes.Buffer
	if err := runSeed([]string{"--dest", blocker}, &stdout, func(string) string { return "" }); err == nil {
		t.Error("runSeed(dest is a file) = nil, want the seed failure")
	}
}

func TestSelectImageRefMatrix(t *testing.T) {
	t.Parallel()

	if got := selectImageRef("", nil); got != "" {
		t.Errorf("selectImageRef(unset, nil getenv) = %q, want empty", got)
	}
	if got := selectImageRef("flag-ref\n", func(string) string { return "env-ref" }); got != "flag-ref" {
		t.Errorf("selectImageRef() = %q, want the flag value with one newline trimmed", got)
	}
	if got := selectImageRef("", func(string) string { return "env-ref\n" }); got != "env-ref" {
		t.Errorf("selectImageRef() = %q, want the environment value with one newline trimmed", got)
	}
}

func TestWriteAtomicFailsClosed(t *testing.T) {
	t.Parallel()

	if err := writeAtomic(filepath.Join(t.TempDir(), "missing-dir", "f"), 0o644, func(io.Writer) error {
		return nil
	}); err == nil {
		t.Error("writeAtomic(uncreatable temp) = nil, want failure")
	}
	boom := errors.New("boom")
	dir := t.TempDir()
	if err := writeAtomic(filepath.Join(dir, "f"), 0o644, func(io.Writer) error {
		return boom
	}); err == nil {
		t.Error("writeAtomic(failing write) = nil, want the write error")
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Errorf("writeAtomic left %v (err %v), want the temp file removed", entries, err)
	}
}
