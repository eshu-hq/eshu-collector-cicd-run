// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	collector "github.com/eshu-hq/eshu-collector-cicd-run"
)

const goodImage = "ghcr.io/eshu-hq/eshu-collector-cicd-run@sha256:" +
	"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func writeFile(t *testing.T, path string, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func TestSeedWritesBinaryAndManifestAndIsIdempotent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	executable := filepath.Join(dir, "self")
	writeFile(t, executable, "binary-bytes-v1", 0o600)
	dest := filepath.Join(dir, "nested", "component")
	manifest := []byte("kind: ComponentPackage\n")

	digest, err := seed(dest, executable, manifest)
	if err != nil {
		t.Fatalf("seed() error = %v", err)
	}
	sum := sha256.Sum256([]byte("binary-bytes-v1"))
	if digest != hex.EncodeToString(sum[:]) {
		t.Fatalf("digest = %s", digest)
	}
	assertSeeded(t, dest, "binary-bytes-v1", string(manifest))

	// A second run, and a run over stale files, converges on the same state.
	writeFile(t, filepath.Join(dest, "collector"), "stale", 0o600)
	writeFile(t, filepath.Join(dest, "manifest.yaml"), "stale", 0o600)
	for range 2 {
		if _, err := seed(dest, executable, manifest); err != nil {
			t.Fatalf("re-seed error = %v", err)
		}
		assertSeeded(t, dest, "binary-bytes-v1", string(manifest))
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("destination holds %d entries, want exactly collector and manifest.yaml", len(entries))
	}
}

func assertSeeded(t *testing.T, dest string, binary string, manifest string) {
	t.Helper()
	info, err := os.Stat(filepath.Join(dest, "collector"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("binary mode = %v, want 0755", info.Mode().Perm())
	}
	got, err := os.ReadFile(filepath.Join(dest, "collector"))
	if err != nil || string(got) != binary {
		t.Fatalf("binary content = %q (err %v)", got, err)
	}
	info, err = os.Stat(filepath.Join(dest, "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("manifest mode = %v, want 0644", info.Mode().Perm())
	}
	got, err = os.ReadFile(filepath.Join(dest, "manifest.yaml"))
	if err != nil || string(got) != manifest {
		t.Fatalf("manifest content = %q (err %v)", got, err)
	}
}

func TestSeedRefusesBadInput(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	executable := filepath.Join(dir, "self")
	writeFile(t, executable, "x", 0o600)
	if _, err := seed("  ", executable, nil); err == nil {
		t.Error("seed() with no destination error = nil")
	}
	if _, err := seed(filepath.Join(dir, "d"), filepath.Join(dir, "missing"), nil); err == nil {
		t.Error("seed() with a missing executable error = nil")
	}
	blocker := filepath.Join(dir, "file")
	writeFile(t, blocker, "x", 0o600)
	if _, err := seed(filepath.Join(blocker, "sub"), executable, nil); err == nil {
		t.Error("seed() under a regular file error = nil")
	}
}

func TestManifestForStampsOnlyADigestPinnedImage(t *testing.T) {
	t.Parallel()

	got, err := manifestFor(collector.ManifestYAML, goodImage)
	if err != nil {
		t.Fatalf("manifestFor() error = %v", err)
	}
	if !bytes.Contains(got, []byte("image: "+goodImage+"\n")) {
		t.Fatalf("stamped manifest lacks the image:\n%s", got)
	}
	if bytes.Contains(got, []byte("sha256:"+strings.Repeat("0", 64))) {
		t.Fatal("the placeholder digest survived stamping")
	}
	if len(got) <= len(collector.ManifestYAML)-200 {
		t.Fatal("stamping must not drop manifest lines")
	}
	unchanged, err := manifestFor(collector.ManifestYAML, "")
	if err != nil || !bytes.Equal(unchanged, collector.ManifestYAML) {
		t.Fatalf("an empty --image-ref must leave the manifest untouched (err %v)", err)
	}
	for _, bad := range []string{
		"ghcr.io/eshu-hq/eshu-collector-cicd-run:latest", "ghcr.io/x@sha256:abc", "ghcr.io/x@sha256:" + strings.Repeat("G", 64),
		"ghcr.io/x@sha256:" + strings.Repeat("A", 64), "a b@sha256:" + strings.Repeat("a", 64), "@sha256:" + strings.Repeat("a", 64),
		"a@@sha256:" + strings.Repeat("a", 64), "ghcr.io/x@sha256:" + strings.Repeat("a", 64) + " trailing",
	} {
		if _, err := manifestFor(collector.ManifestYAML, bad); err == nil {
			t.Errorf("manifestFor(%q) error = nil, want rejection", bad)
		}
	}
	if _, err := manifestFor([]byte("spec: {}\n"), goodImage); err == nil {
		t.Error("a manifest with no artifact image must not be stamped")
	}
}

func TestEmbeddedManifestMatchesTheRepositoryManifest(t *testing.T) {
	t.Parallel()

	onDisk, err := os.ReadFile(filepath.Join("..", "..", "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(onDisk, collector.ManifestYAML) {
		t.Fatal("embedded manifest differs from manifest.yaml")
	}
}

// TestSeedFromTheBuiltBinary builds the real static binary and runs its seed
// subcommand, proving the init-container contract end to end: the seeded
// collector is byte-identical to the running binary and the manifest is the
// embedded one, with no shell involved.
func TestSeedFromTheBuiltBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the collector binary")
	}
	t.Parallel()

	dir := t.TempDir()
	binary := filepath.Join(dir, "collector-built")
	build := exec.CommandContext(context.Background(), "go", "build", "-trimpath", "-o", binary, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build error = %v\n%s", err, output)
	}
	dest := filepath.Join(dir, "home", "component")
	seedCmd := exec.CommandContext(context.Background(), binary, "seed", "--dest", dest, "--image-ref", goodImage)
	if output, err := seedCmd.CombinedOutput(); err != nil {
		t.Fatalf("seed error = %v\n%s", err, output)
	}
	built, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	seeded, err := os.ReadFile(filepath.Join(dest, "collector"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(built, seeded) {
		t.Fatal("seeded collector differs from the running binary")
	}
	manifest, err := os.ReadFile(filepath.Join(dest, "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(manifest, []byte(goodImage)) {
		t.Fatalf("seeded manifest lacks the stamped image:\n%s", manifest)
	}
	// The seeded binary is itself runnable: it rejects empty stdin without a shell.
	run := exec.CommandContext(context.Background(), filepath.Join(dest, "collector"))
	run.Stdin = strings.NewReader("")
	if err := run.Run(); err == nil {
		t.Fatal("seeded collector accepted empty stdin")
	}
}

// TestStampImageReplacesLiterally calls the replacement directly with a value
// the pattern would never admit, so the literal-replacement guarantee does not
// rest on the pattern alone: a template replacement would expand "$1" and
// "${0}" inside the reference.
func TestStampImageReplacesLiterally(t *testing.T) {
	t.Parallel()

	ref := "reg.invalid/$1/${0}@sha256:" + strings.Repeat("b", 64)
	got, err := stampImage(collector.ManifestYAML, ref)
	if err != nil {
		t.Fatalf("stampImage() error = %v", err)
	}
	if !bytes.Contains(got, []byte("image: "+ref+"\n")) {
		t.Fatalf("the reference was not written literally:\n%s", got)
	}
}

func TestSeedImageRefFlagWinsOverEnvironment(t *testing.T) {
	t.Parallel()

	envRef := "ghcr.io/env/cicd-run@sha256:" + strings.Repeat("e", 64)
	flagRef := "ghcr.io/flag/cicd-run@sha256:" + strings.Repeat("f", 64)
	run := func(t *testing.T, args []string, env string) (string, error) {
		t.Helper()
		dest := filepath.Join(t.TempDir(), "component")
		var out bytes.Buffer
		err := runSeed(append([]string{"--dest", dest}, args...), &out, func(key string) string {
			if key == imageRefEnv {
				return env
			}
			return ""
		})
		if err != nil {
			return "", err
		}
		manifest, readErr := os.ReadFile(filepath.Join(dest, "manifest.yaml"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		return string(manifest), nil
	}
	if got, err := run(t, nil, envRef); err != nil || !strings.Contains(got, envRef) {
		t.Fatalf("env only: err=%v, want the environment reference stamped", err)
	}
	if got, err := run(t, []string{"--image-ref", flagRef}, envRef); err != nil || !strings.Contains(got, flagRef) || strings.Contains(got, envRef) {
		t.Fatalf("flag and env: err=%v, want the flag to win", err)
	}
	if got, err := run(t, nil, ""); err != nil || !strings.Contains(got, strings.Repeat("0", 64)) {
		t.Fatalf("empty env: err=%v, want the repository placeholder unchanged", err)
	}
	if _, err := run(t, nil, "  "); err == nil {
		t.Fatal("a blank but non-empty environment reference must fail, not be ignored")
	}
	if _, err := run(t, nil, "not-a-digest-ref"); err == nil {
		t.Fatal("an invalid environment reference must fail, not be ignored")
	}
	if _, err := run(t, []string{"--image-ref", "ghcr.io/x:latest"}, envRef); err == nil {
		t.Fatal("an invalid flag reference must fail even when the environment is valid")
	}
	if _, err := run(t, []string{"--image", flagRef}, ""); err == nil {
		t.Fatal("the old --image flag must no longer be accepted")
	}
}
