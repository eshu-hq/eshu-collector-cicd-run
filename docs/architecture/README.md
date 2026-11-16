# Architecture

## Where it sits in Eshu

This collector is a satellite. It runs outside Eshu core, as its own little program, and the two meet at exactly one narrow seam: the host starts it per claim, talks `collector-sdk/v1alpha1` over stdin and stdout, and takes responsibility for everything after the result comes back. The [system context diagram](../diagrams/README.md#system-context) shows that seam, and the [claim lifecycle](../diagrams/README.md#claim-lifecycle) shows a single exchange across it.

That narrowness is the point. The collector can be wrong, slow, or downright broken, and the worst it can do is fail a claim. It can't write to Eshu's stores, because nothing here knows how.

## What it's made of

The workhorse is the `producer/` package: provider fetching and normalization for GitHub and GitLab, fact emission, and the window, warning, and redaction rules. `cmd/collector` is the entry point and the seed command. One quirk sets this collector apart: it reads its activation from the process environment, not from the request. `ESHU_COLLECTOR_INSTANCES_JSON` holds the instance list (the operator shape shared with the built-in collector), the handler selects the one enabled, claim-capable `ci_cd_run` instance, and only the per-claim host token for `host_minted` targets arrives on the request config. Each claim is bound to its provider target and checked (kind, instance, scope, generation, fencing) before anything is fetched or emitted. The host owns admission, grants, claim identity, fact validation, and process supervision. The collector gets no database, graph, queue, or workflow handle. Lease and heartbeat settings also load from the environment (`ESHU_CICD_RUN_CLAIM_LEASE_TTL`, default 60s; `ESHU_CICD_RUN_HEARTBEAT_INTERVAL`, default 20s), and the heartbeat must be shorter than the lease or startup fails. All 9 fact kinds belong to Eshu core, so the host refuses them without a producer grant. [grant.example.json](../../grant.example.json) lists the 9 requests, each scoped to `ci_cd_run`. This repository only ships the request and never emits past host grant admission.

## Tech stack

The binary is a static, pure-Go build. The [Dockerfile](../../Dockerfile) compiles `./cmd/collector` with `CGO_ENABLED=0` and `-trimpath` on `golang:1.26.11-alpine`, then copies the result onto a distroless static base (`gcr.io/distroless/static-debian12:nonroot`) with no shell and no package manager. It runs as user `65532`, and the entrypoint serves one claim from stdin by default (`--sdk-stdio`), with `seed --dest DIR` as the self-install path an init container uses.

## Versions it pins

Exact versions from `go.mod`. Please don't substitute another SDK line.

| Module | Version |
| --- | --- |
| Go | `1.26.11` |
| `github.com/eshu-hq/eshu-sdk/collector` | `v0.2.0-rc.1` |
| `github.com/eshu-hq/eshu-sdk/factschema` | `v0.1.0-rc.5` |
| `gopkg.in/yaml.v3` | `v3.0.1` |

The component ID is `dev.eshu.collector.cicd-run`, version `0.1.2`, collector kind `ci_cd_run`. It declares support for Eshu core `>=0.0.5 <0.2.0` and speaks `collector-sdk/v1alpha1` over the process adapter, all recorded in [manifest.yaml](../../manifest.yaml). Check that range against your core version before enabling it. A mismatch makes the host refuse the component.
