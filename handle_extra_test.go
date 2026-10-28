// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/eshu-hq/eshu-collector-cicd-run/producer"
)

// TestFetchWindowGitLabUsesGitLabClient proves the GitLab dispatch arm:
// an empty pipeline list is an unchanged generation, not an error.
func TestFetchWindowGitLabUsesGitLabClient(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/projects/eshu-hq%2Fdemo/pipelines" {
			t.Errorf("unexpected path %s", r.URL.EscapedPath())
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(server.Close)

	handler := Handler{HTTPClient: server.Client()}
	target := producer.ResolvedTarget{
		Provider:   producer.ProviderGitLabCI,
		ScopeID:    "gitlab-ci://gitlab.com/eshu-hq/demo",
		Repository: "eshu-hq/demo",
		APIBaseURL: server.URL,
		MaxRuns:    1,
		MaxJobs:    25,
	}
	claim := handlerClaim()
	claim.Scope.ID = target.ScopeID
	window, err := handler.fetchWindow(context.Background(), claim, target, "fetch-token")
	if err != nil {
		t.Fatalf("fetchWindow(gitlab) error = %v, want nil", err)
	}
	if window.Provider != producer.ProviderGitLabCI {
		t.Errorf("window.Provider = %q, want the GitLab client dispatch", window.Provider)
	}
	if len(window.Evidences) != 0 {
		t.Errorf("len(Evidences) = %d, want 0 for the empty pipeline list", len(window.Evidences))
	}
}

// TestLogFailureNilLogIsSilent proves the diagnostics seam never faults
// a claim when no log sink is attached.
func TestLogFailureNilLogIsSilent(t *testing.T) {
	t.Parallel()

	var handler Handler
	handler.logFailure(handlerClaim(), errors.New("boom"))
}
