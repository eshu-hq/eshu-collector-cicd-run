// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"os"
	"reflect"
	"testing"

	"github.com/eshu-hq/eshu-collector-cicd-run/producer"
	"gopkg.in/yaml.v3"
)

// componentManifest mirrors the structural fields of manifest.yaml the
// satellite contract depends on: identity, version, runtime, artifacts,
// emitted facts, and the telemetry prefix. It is intentionally local:
// the component Manifest type is core-owned and must never be imported
// here (see boundary_test.go).
type componentManifest struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		ID        string `yaml:"id"`
		Name      string `yaml:"name"`
		Publisher string `yaml:"publisher"`
		Version   string `yaml:"version"`
	} `yaml:"metadata"`
	Spec struct {
		CompatibleCore string   `yaml:"compatibleCore"`
		ComponentType  string   `yaml:"componentType"`
		CollectorKinds []string `yaml:"collectorKinds"`
		Runtime        struct {
			SDKProtocol string `yaml:"sdkProtocol"`
			Adapter     string `yaml:"adapter"`
		} `yaml:"runtime"`
		Artifacts []struct {
			Platform string `yaml:"platform"`
			Image    string `yaml:"image"`
		} `yaml:"artifacts"`
		EmittedFacts []struct {
			Kind             string   `yaml:"kind"`
			PayloadSchemaRef string   `yaml:"payloadSchemaRef"`
			SchemaVersions   []string `yaml:"schemaVersions"`
			SourceConfidence []string `yaml:"sourceConfidence"`
		} `yaml:"emittedFacts"`
		Telemetry struct {
			MetricsPrefix string `yaml:"metricsPrefix"`
		} `yaml:"telemetry"`
	} `yaml:"spec"`
}

func loadManifest(t *testing.T) componentManifest {
	t.Helper()

	raw, err := os.ReadFile("manifest.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var manifest componentManifest
	if err := yaml.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("manifest.yaml: %v", err)
	}
	return manifest
}

// TestManifestMatchesProducerContract keeps the shipped manifest, the Go
// contract, and the producer seam on one identity: the manifest id is the
// producer id the grant will bind, its version is the producer version
// the release tag must match, its kinds are exactly the nine emitted
// ci.* kinds at schema 1.0.0 with reported confidence, and its runtime
// names the SDK protocol the binary serves.
func TestManifestMatchesProducerContract(t *testing.T) {
	t.Parallel()

	manifest := loadManifest(t)
	if ComponentID != producer.ProducerID {
		t.Errorf("ComponentID = %q, want producer identity %q", ComponentID, producer.ProducerID)
	}
	if manifest.Metadata.ID != ComponentID {
		t.Errorf("manifest id = %q, want %q (producer identity)", manifest.Metadata.ID, ComponentID)
	}
	if manifest.Metadata.Version != producer.ProducerVersion {
		t.Errorf("manifest version = %q, want producer version %q", manifest.Metadata.Version, producer.ProducerVersion)
	}
	if !reflect.DeepEqual(manifest.Spec.CollectorKinds, []string{CollectorKind}) {
		t.Errorf("collectorKinds = %v, want [%s]", manifest.Spec.CollectorKinds, CollectorKind)
	}
	if manifest.Spec.Runtime.SDKProtocol != producer.Contract().ProtocolVersion {
		t.Errorf("sdkProtocol = %q, want contract protocol %q",
			manifest.Spec.Runtime.SDKProtocol, producer.Contract().ProtocolVersion)
	}
	if manifest.Spec.Runtime.Adapter != "process" {
		t.Errorf("adapter = %q, want process", manifest.Spec.Runtime.Adapter)
	}
	if manifest.Spec.Telemetry.MetricsPrefix != MetricsPrefix {
		t.Errorf("metricsPrefix = %q, want %q", manifest.Spec.Telemetry.MetricsPrefix, MetricsPrefix)
	}
	if len(manifest.Spec.Artifacts) == 0 {
		t.Error("manifest declares no artifacts")
	}
	// schemalessKinds are registry-declared ci kinds with no shipped
	// fixture-pack schema (eshu-hq/eshu#9329). They must declare no
	// payloadSchemaRef: the field is omitempty by design and empty means
	// "registry kind with no payload schema" in every consumer. Every
	// other emitted kind must reference its own fixture-pack schema key.
	schemalessKinds := map[string]bool{
		"ci.pipeline_definition": true,
		"ci.job":                 true,
		"ci.warning":             true,
	}
	var kinds []string
	for _, family := range manifest.Spec.EmittedFacts {
		kinds = append(kinds, family.Kind)
		if schemalessKinds[family.Kind] {
			if family.PayloadSchemaRef != "" {
				t.Errorf("kind %s payloadSchemaRef = %q, want empty (no fixture-pack schema ships for this kind)",
					family.Kind, family.PayloadSchemaRef)
			}
		} else if family.PayloadSchemaRef != family.Kind {
			t.Errorf("kind %s payloadSchemaRef = %q, want the kind (fixturepack schema key)",
				family.Kind, family.PayloadSchemaRef)
		}
		if !reflect.DeepEqual(family.SchemaVersions, []string{producer.SchemaVersion}) {
			t.Errorf("kind %s schemaVersions = %v, want [%s]",
				family.Kind, family.SchemaVersions, producer.SchemaVersion)
		}
		if !reflect.DeepEqual(family.SourceConfidence, []string{"reported"}) {
			t.Errorf("kind %s sourceConfidence = %v, want [reported]", family.Kind, family.SourceConfidence)
		}
	}
	if !reflect.DeepEqual(kinds, FactKinds()) {
		t.Errorf("manifest kinds = %v, want FactKinds() %v", kinds, FactKinds())
	}
}

// TestManifestEmbedMatchesFile proves the binary embeds the checked-in
// manifest: seed must ship exactly what the repository declares.
func TestManifestEmbedMatchesFile(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("manifest.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if string(ManifestYAML) != string(raw) {
		t.Error("embedded ManifestYAML differs from manifest.yaml; rebuild after editing the manifest")
	}
}
