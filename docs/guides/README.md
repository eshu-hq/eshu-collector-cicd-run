# Guides

Step-by-step help for the jobs you'll actually do with this collector. Each one assumes you've skimmed [getting started](../getting-started/README.md).

## Seed it into a component folder

The published image is `ghcr.io/eshu-hq/eshu-collector-cicd-run`. It holds one static binary with no shell, and the binary can install itself into a component folder, which is how a startup (init) container usually sets it up:

```bash
collector seed --dest /component [--image-ref ghcr.io/eshu-hq/eshu-collector-cicd-run@sha256:<digest>]
```

That writes `/component/collector` (mode 0755) and `/component/manifest.yaml`. The `--image-ref` flag, or the `COLLECTOR_IMAGE_REF` environment variable when the flag is absent, stamps the exact deployed image reference into the manifest. The flag wins when both are set. This step exists because an image can't contain its own digest, so the manifest in this repository keeps an all-zero placeholder digest on purpose. A bad image reference fails before any file is written. The [deployment diagram](../diagrams/README.md#deployment-and-seed) draws the whole flow.

## Operate it day to day

The main signal is the claim result and its failure class. A claim this collector can't serve ends terminal with `invalid_config.<reason>`, and the bare bucket is never emitted, so the reason always names something you can act on. The full list:

| Failure class | Meaning |
| --- | --- |
| `invalid_config.activation` | Activation cannot load, or unknown provider or auth mode |
| `invalid_config.claim_binding` | Claim kind, instance, generation, or fencing does not match |
| `invalid_config.target_scope_mismatch` | Claim scope has no registered target |
| `invalid_config.missing_host_token` | `host_minted` claim arrived without the host token object |
| `invalid_config.empty_host_token` | Host token object arrived with a blank token |
| `invalid_config.host_token_shape` | Host token is not the `{token, scope_id}` object |
| `invalid_config.host_token_scope_mismatch` | Token `scope_id` differs from the target scope |
| `invalid_config.github_app_mint_failed` | Direct-App mint was refused |
| `invalid_config.provider_denied` | Provider answered 401/403: credential refused for this scope |
| `invalid_config.provider_not_found` | Provider answered 404: repository, run, or pipeline missing |
| `invalid_config.provider_rejected` | Any other fixed provider refusal |

The collector writes one bounded diagnostic line per failed claim to standard error. The host keeps only the byte count, so that line helps only in local runs and tests. This repository emits no metrics. The manifest declares the prefix `eshu_dp_cicd_run_collector_`, but nothing is emitted under it, and the `eshu_dp_ci_cd_run_*` signal names preserved in `producer/telemetry.go` stay host-side.

## Troubleshoot a failing feed

If claims end at `invalid_config.target_scope_mismatch`, no registered target serves that claim scope, so check the instance JSON in `ESHU_COLLECTOR_INSTANCES_JSON` first rather than the request. The `missing_`, `empty_`, `shape`, and `scope_mismatch` host-token reasons each point at a different way the per-claim token object can be wrong, and all of them are wiring faults on the host side. `provider_denied` and `provider_not_found` are the provider answering 401/403 or 404, so check the credential's repository access before anything else. And if a repository looks oddly quiet, remember that an unchanged or empty window returns `unchanged` with no fact writes. That's expected, not an outage.

## Release a new version

Releases run from `.github/workflows/release.yml` on every push to `main` and on version tags. Nothing runs for a pull request, and the workflow deploys nothing.

To publish, tag `v<manifest version>` on the protected `main` head. The workflow builds, pushes, signs, and attests the image, and a tag on any other commit fails before anything is built. Copy the digest from the job summary into the activation's seed image. Pin real deployments to digests rather than moving tags.
