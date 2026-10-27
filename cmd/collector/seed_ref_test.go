// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	collector "github.com/eshu-hq/eshu-collector-cicd-run"
)

var digest64 = strings.Repeat("ab12", 16)

// placeholderRef is the artifact image line value the repository manifest
// carries.
const placeholderRef = "ghcr.io/eshu-hq/eshu-collector-cicd-run@sha256:" +
	"0000000000000000000000000000000000000000000000000000000000000000"

// hostileRefs must each exit non-zero and write nothing. They carry
// characters that mean something to YAML, to a regexp replacement template, or
// to a shell, or break the one-trailing-newline rule.
var hostileRefs = map[string]string{
	"dollar group":       "a$1b@sha256:" + digest64,
	"dollar brace":       "a${0}b@sha256:" + digest64,
	"double quote":       `a"b@sha256:` + digest64,
	"single quote":       "a'b@sha256:" + digest64,
	"hash":               "a#b@sha256:" + digest64,
	"open bracket":       "a[b@sha256:" + digest64,
	"close bracket":      "a]b@sha256:" + digest64,
	"brace":              "a{b}@sha256:" + digest64,
	"colon space":        "a: b@sha256:" + digest64,
	"ampersand":          "a&b@sha256:" + digest64,
	"asterisk":           "*a@sha256:" + digest64,
	"tab":                "a\tb@sha256:" + digest64,
	"carriage return":    "a@sha256:" + digest64 + "\r",
	"two trailing lines": "a@sha256:" + digest64 + "\n\n",
	"embedded newline":   "a\nb@sha256:" + digest64,
	"leading space":      " a@sha256:" + digest64,
	"trailing space":     "a@sha256:" + digest64 + " ",
	"uppercase digest":   "a@sha256:" + strings.ToUpper(digest64),
	"uppercase name":     "A@sha256:" + digest64,
	"short digest":       "a@sha256:" + digest64[:63],
	"long digest":        "a@sha256:" + digest64 + "0",
	"tag only":           "ghcr.io/eshu-hq/x:latest",
	"tag and digest":     "a:tag@sha256:" + digest64,
	"empty segment":      "a//b@sha256:" + digest64,
	"leading dash":       "-a@sha256:" + digest64,
	"double separator":   "a..b@sha256:" + digest64,
	"no name":            "@sha256:" + digest64,
}

func seedWith(t *testing.T, flagRef string, envRef string) (dest string, err error) {
	t.Helper()
	dest = filepath.Join(t.TempDir(), "component")
	args := []string{"--dest", dest}
	if flagRef != "" {
		args = append(args, "--image-ref", flagRef)
	}
	err = runSeed(args, &bytes.Buffer{}, func(key string) string {
		if key == imageRefEnv {
			return envRef
		}
		return ""
	})
	return dest, err
}

// TestSeedRejectsHostileRefsAndWritesNothing proves, through both the flag and
// the environment variable, that a malformed reference exits with an error
// before the destination is touched and that the error never echoes the value.
func TestSeedRejectsHostileRefsAndWritesNothing(t *testing.T) {
	t.Parallel()

	for name, ref := range hostileRefs {
		for _, via := range []string{"flag", "env"} {
			t.Run(name+"/"+via, func(t *testing.T) {
				t.Parallel()
				flagRef, envRef := ref, ""
				if via == "env" {
					flagRef, envRef = "", ref
				}
				dest, err := seedWith(t, flagRef, envRef)
				if err == nil {
					t.Fatalf("seed accepted %q", ref)
				}
				if trimmed := strings.TrimSpace(ref); trimmed != "" && strings.Contains(err.Error(), trimmed) {
					t.Fatalf("error echoes the reference: %v", err)
				}
				if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
					t.Fatalf("a rejected reference must write nothing, but %s exists (stat error %v)", dest, statErr)
				}
			})
		}
	}
}

// TestSeedStampsValidRefsByteExactly compares the seeded manifest with the
// embedded one: the only difference is the artifact image value.
func TestSeedStampsValidRefsByteExactly(t *testing.T) {
	t.Parallel()

	for name, ref := range map[string]string{
		"registry with port": "registry.example:5000/team/collector@sha256:" + digest64,
		"single segment":     "collector@sha256:" + digest64,
		"ghcr nested":        "ghcr.io/eshu-hq/eshu-collector-cicd-run@sha256:" + digest64,
		"separators":         "reg.example/a_b/c-d.e@sha256:" + digest64,
	} {
		want := bytes.Replace(collector.ManifestYAML, []byte(placeholderRef), []byte(ref), 1)
		if bytes.Equal(want, collector.ManifestYAML) {
			t.Fatal("the placeholder image line was not found in the embedded manifest")
		}
		for _, tc := range []struct{ via, flagRef, envRef string }{
			{"flag", ref, ""},
			{"env", "", ref},
			{"env with one trailing newline", "", ref + "\n"},
			{"flag with one trailing newline", ref + "\n", ""},
		} {
			t.Run(name+"/"+tc.via, func(t *testing.T) {
				t.Parallel()
				dest, err := seedWith(t, tc.flagRef, tc.envRef)
				if err != nil {
					t.Fatalf("seed error = %v", err)
				}
				got, err := os.ReadFile(filepath.Join(dest, "manifest.yaml"))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("seeded manifest differs from the embedded one beyond the image value:\n%s", got)
				}
			})
		}
	}
}

// TestSeedBuiltBinaryRefusesHostileRefs runs the real binary, so the exit code
// is the process exit code and not a returned error.
func TestSeedBuiltBinaryRefusesHostileRefs(t *testing.T) {
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
	for _, name := range []string{"dollar group", "double quote", "hash", "two trailing lines"} {
		ref := hostileRefs[name]
		dest := filepath.Join(dir, strings.ReplaceAll(name, " ", "-"))
		for _, env := range [][]string{nil, {imageRefEnv + "=" + ref}} {
			args := []string{"seed", "--dest", dest}
			if env == nil {
				args = append(args, "--image-ref", ref)
			}
			cmd := exec.CommandContext(context.Background(), binary, args...)
			cmd.Env = append(os.Environ(), env...)
			output, err := cmd.CombinedOutput()
			var exitErr *exec.ExitError
			if err == nil || !errors.As(err, &exitErr) || exitErr.ExitCode() == 0 {
				t.Fatalf("%s: seed exit error = %v, want a non-zero exit\n%s", name, err, output)
			}
			if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
				t.Fatalf("%s: a rejected reference wrote %s", name, dest)
			}
		}
	}
}
