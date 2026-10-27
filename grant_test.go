// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"
)

// producerGrant is the shape of a core producer grant request. Core issues the
// grant; this repository stores only the request an operator submits.
type producerGrant struct {
	ProducerID     string    `json:"producer_id"`
	Version        string    `json:"version"`
	Kind           string    `json:"kind"`
	SchemaVersions []string  `json:"schema_versions"`
	Scope          string    `json:"scope"`
	ExpiresAt      time.Time `json:"expires_at"`
	Revoked        bool      `json:"revoked"`
}

// TestGrantExampleCoversExactlyTheManifestKinds proves the grant request an
// operator submits matches the manifest: one grant per declared core kind,
// bound to this producer and version, scoped to the manifest's collector kind
// (core matches a grant's scope against the declared collector kinds), and
// covering every declared schema version.
func TestGrantExampleCoversExactlyTheManifestKinds(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("grant.example.json")
	if err != nil {
		t.Fatal(err)
	}
	var grants []producerGrant
	if err := json.Unmarshal(raw, &grants); err != nil {
		t.Fatalf("grant.example.json: %v", err)
	}
	manifest := loadManifest(t)
	declared := map[string][]string{}
	for _, family := range manifest.Spec.EmittedFacts {
		declared[family.Kind] = family.SchemaVersions
	}
	got := map[string][]string{}
	for _, grant := range grants {
		if grant.ProducerID != manifest.Metadata.ID || grant.Version != manifest.Metadata.Version {
			t.Fatalf("grant %+v is not bound to %s@%s", grant, manifest.Metadata.ID, manifest.Metadata.Version)
		}
		if grant.Scope != manifest.Spec.CollectorKinds[0] || grant.Revoked || grant.ExpiresAt.IsZero() {
			t.Fatalf("grant %+v must be live and scoped to the collector kind %q", grant, manifest.Spec.CollectorKinds[0])
		}
		if _, dup := got[grant.Kind]; dup {
			t.Fatalf("grant kind %s appears twice", grant.Kind)
		}
		got[grant.Kind] = grant.SchemaVersions
	}
	var missing, extra []string
	for kind := range declared {
		if _, ok := got[kind]; !ok {
			missing = append(missing, kind)
		}
	}
	for kind := range got {
		if _, ok := declared[kind]; !ok {
			extra = append(extra, kind)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("grant kinds diverge from the manifest: missing %v, extra %v", missing, extra)
	}
	for kind, versions := range declared {
		if !reflect.DeepEqual(got[kind], versions) {
			t.Errorf("grant kind %s schema versions = %v, want manifest %v", kind, got[kind], versions)
		}
	}
}
