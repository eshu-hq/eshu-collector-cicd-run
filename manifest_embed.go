// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import _ "embed"

// ManifestYAML is the component manifest shipped with this binary. The seed
// subcommand writes it next to the binary so an init container can hand the
// host a matching manifest without a shell or the repository checkout.
//
//go:embed manifest.yaml
var ManifestYAML []byte
