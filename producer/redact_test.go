// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package producer

import (
	"testing"
)

// URL sanitizer contract: credential- or query-bearing values never reach a
// persisted source_ref or redacted free text. Each vector drives the real
// stripSensitiveURL/redactSensitiveText/sanitizeDeploymentURL path and
// asserts the persisted outcome, not the helper's spelling.
func TestStripSensitiveURLDropsCredentialsAndQueries(t *testing.T) {
	t.Parallel()
	vectors := []struct {
		name  string
		value string
		want  string
	}{
		{"clean https passes through", "https://gitlab.example.com/group/project", "https://gitlab.example.com/group/project"},
		{"empty stays empty", "", ""},
		{"userinfo is dropped", "https://user:secret@gitlab.example.com/group/project", ""},
		{"query string is dropped", "https://gitlab.example.com/group/project?token=abc", ""},
		{"fragment alone passes through", "https://gitlab.example.com/group/project#section", "https://gitlab.example.com/group/project#section"},
		{"opaque credential is dropped", "svc:SECRET@host/x", ""},
		{"unparseable is dropped", "http://[::1", ""},
		{"surrounding whitespace is trimmed", "  https://gitlab.example.com/group/project  ", "https://gitlab.example.com/group/project"},
	}
	for _, vector := range vectors {
		t.Run(vector.name, func(t *testing.T) {
			t.Parallel()
			if got := stripSensitiveURL(vector.value); got != vector.want {
				t.Fatalf("stripSensitiveURL(%q) = %q, want %q", vector.value, got, vector.want)
			}
		})
	}
}

func TestCarriesOpaqueUserinfoFlagsAuthorityCredentials(t *testing.T) {
	t.Parallel()
	vectors := []struct {
		name string
		raw  string
		want bool
	}{
		{"opaque credential before path", "svc:SECRET@host/x", true},
		{"opaque credential without path", "svc:SECRET@host", true},
		{"hierarchical url has no opaque userinfo", "https://gitlab.example.com/group/project", false},
		{"plain path has none", "group/project", false},
		{"at after path separator is not userinfo", "scheme:opaque/path@host", false},
		{"at after query cut is not userinfo", "scheme:opaque?x@host", false},
		{"empty has none", "", false},
	}
	for _, vector := range vectors {
		t.Run(vector.name, func(t *testing.T) {
			t.Parallel()
			if got := carriesOpaqueUserinfo(vector.raw); got != vector.want {
				t.Fatalf("carriesOpaqueUserinfo(%q) = %v, want %v", vector.raw, got, vector.want)
			}
		})
	}
}

func TestRedactSensitiveTextReplacesCredentialedURLs(t *testing.T) {
	t.Parallel()
	got := redactSensitiveText("see https://user:secret@gitlab.example.com/group/project for detail")
	want := "see [redacted_url] for detail"
	if got != want {
		t.Fatalf("redactSensitiveText() = %q, want %q", got, want)
	}
	clean := "see https://gitlab.example.com/group/project for detail"
	if got := redactSensitiveText(clean); got != clean {
		t.Fatalf("redactSensitiveText(clean) = %q, want unchanged", got)
	}
}

func TestSanitizeDeploymentURLStripsQueryFragmentAndUser(t *testing.T) {
	t.Parallel()
	vectors := []struct {
		name string
		raw  string
		want string
	}{
		{"clean url passes through", "https://example.com/deploy/1", "https://example.com/deploy/1"},
		{"query and fragment are stripped", "https://example.com/deploy/1?token=abc#frag", "https://example.com/deploy/1"},
		{"userinfo is stripped", "https://user:secret@example.com/deploy/1", "https://example.com/deploy/1"},
		{"empty stays empty", "", ""},
		{"unparseable yields empty", "http://[::1", ""},
		{"opaque credential yields empty", "svc:SECRET@host/x", ""},
	}
	for _, vector := range vectors {
		t.Run(vector.name, func(t *testing.T) {
			t.Parallel()
			if got := sanitizeDeploymentURL(vector.raw); got != vector.want {
				t.Fatalf("sanitizeDeploymentURL(%q) = %q, want %q", vector.raw, got, vector.want)
			}
		})
	}
}
