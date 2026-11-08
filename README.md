# eshu-collector-cicd-run

Status: pre-release. Do not rely on it in production yet.

This collector reads recent CI/CD runs (automated build and deploy jobs) from GitHub Actions or GitLab CI and hands Eshu a checked summary of what ran.

Eshu is the core system. A collector is a small helper program: it reads one outside system, turns what it finds into facts (small, typed records), and gives those facts back to Eshu. CI/CD is the automated pipeline that builds, tests, and deploys code; a run is one execution of that pipeline, made of jobs and steps. This collector only reads run history. It never starts, cancels, or changes a run.

A claim is one unit of work Eshu sends the collector. An activation is one configured copy of this collector, set up with one or more provider targets.

## What it collects

One run covers one repository target, named by the claim. For that target the collector itself fetches a bounded recent window from the provider — no host fetch loop feeds it:

- GitHub Actions: recent workflow runs with their jobs, steps, and artifacts, plus deployments and their statuses.
- GitLab CI: recent pipelines with their jobs and artifacts.

It returns 9 fact kinds, all at schema `1.0.0` with confidence `reported` (the provider said so; Eshu did not check it directly):

| Fact kind | One fact describes |
| --- | --- |
| `ci.pipeline_definition` | one pipeline or workflow definition |
| `ci.run` | one pipeline run |
| `ci.job` | one job inside a run |
| `ci.step` | one step inside a job |
| `ci.artifact` | one file a run produced |
| `ci.trigger_edge` | one "this event started that run" link |
| `ci.environment_observation` | one observed deployment environment |
| `ci.deployment_event` | one deployment and its outcome |
| `ci.warning` | one non-fatal problem, such as a window cut short by a limit |

GitHub targets can produce all nine kinds; GitLab targets produce the run, job, artifact, and warning kinds. Web addresses and free text are cleaned before anything leaves the collector, and credential-bearing addresses are dropped outright.

A run ends as `complete`, `partial` with a warning (a limit or a mid-window throttle cut the window short, but what was fetched is committed), `unchanged`, `retryable` (try again later), or `terminal` (stop; a person must fix something first). A cancelled claim returns no result at all. A failed run returns no facts.

## How data flows

```mermaid
flowchart LR
  A["GitHub Actions or GitLab CI"] --> B["This collector"] --> C["Eshu host process"] --> D["Eshu core"]
```

Text version: GitHub Actions or GitLab CI → this collector → Eshu host process (checks and stores the result) → Eshu core. The collector never talks to Eshu's database directly.

## When to use it, and when not to

Use it when Eshu needs current build and deploy activity for repositories you name, read on a schedule Eshu controls. Do not use it to trigger or manage pipelines, to read run logs (it reads run structure and outcomes, not log text), or as a general GitHub or GitLab API tool. It also never emits workflow-file image evidence (`ci.workflow_image_evidence`); that kind belongs to another collector.

## How Eshu runs it

Eshu's component host starts this program for one claim at a time: it sends one bounded `collector-sdk/v1alpha1` request on standard input (stdin) and reads one result from standard output (stdout). Requests and results are each capped at 64 MiB; keep those in step with the host's `process.stdout_limit_bytes` and `config.limits.maxResultBytes` (see `config.example.yaml`).

```bash
collector --sdk-stdio   # serve one claim from stdin
```

Unlike most collectors, this one reads its activation from the process environment, not from the request: `ESHU_COLLECTOR_INSTANCES_JSON` holds the instance list (the operator shape shared with the built-in collector), and the handler selects the one enabled, claim-capable `ci_cd_run` instance and resolves the credential references its targets name. Only the per-claim host token (for `host_minted` targets) arrives on the request config. The claim is bound to its provider target and checked — kind, instance, scope, generation, and fencing — before anything is fetched or emitted. The host owns admission, grants, claim identity, fact validation, and process supervision; the collector gets no database, graph, queue, or workflow handle.

The published image is `ghcr.io/eshu-hq/eshu-collector-cicd-run`. It holds one static binary with no shell, and the binary can install itself into a component folder (how a startup, or init, container sets it up):

```bash
collector seed --dest /component [--image-ref ghcr.io/eshu-hq/eshu-collector-cicd-run@sha256:<digest>]
```

This writes `/component/collector` (mode 0755) and `/component/manifest.yaml`. `--image-ref` (or the `COLLECTOR_IMAGE_REF` environment variable; the flag wins) stamps the exact deployed image reference into the manifest, because an image cannot contain its own digest. The manifest in this repository keeps an all-zero placeholder digest on purpose.

## Configuration

Copy `config.example.yaml`. It has three parts: `host` names the job identity Eshu plans, `process` says how Eshu starts the binary, and `config.targets` holds this collector's provider targets. One activation covers the targets of one `ci_cd_run` instance; each target keeps its own scope ID.

| Target key | Meaning |
| --- | --- |
| `provider` | `github_actions` (default when omitted) or `gitlab_ci` |
| `scope_id` | The claim scope this target serves; part of every stored fact key, so choose it once and keep it |
| `repository` | The `owner/repo` (GitHub) or project path (GitLab) to read |
| `auth_mode` | `token_env` (default), `host_minted`, or `github_app`. See Credentials |
| `token_env` | For `token_env` targets: name of the environment variable holding the token |
| `github_app_id_env`, `github_app_installation_id_env`, `github_app_private_key_env` | For `github_app` targets only: names of the variables holding the App ID, installation ID, and private key |
| `allowed_repositories` | The only repositories this target may read |
| `api_base_url` | Provider API address, for example `https://api.github.com` |
| `max_runs`, `max_jobs`, `max_artifacts`, `max_deployments` | Window sizes. Defaults: 10 runs, 30 jobs, 30 artifacts, 10 deployments; hard caps: 100, 500, 500, 100. `0` means "use the default". (`max_artifacts` is ignored for GitLab) |
| `source_uri` | Optional provenance address recorded on facts |

`host_token_file` was removed: setting it in any mode fails at load, with guidance to use `host_minted` instead. Credential shapes never mix on one target; mixed or unknown shapes fail at load, naming the env reference but never its value.

## Credentials

Secrets never go in the config file. Each `github_actions` target picks exactly one shape with `auth_mode`; `gitlab_ci` targets serve `token_env` only:

- `token_env` (default): a static token. `token_env` names an environment variable the worker process already has — for example, `token_env: GITHUB_TOKEN` means "read the token from the variable called `GITHUB_TOKEN`." It is sent as `Authorization: Bearer`.
- `host_minted`: nothing is resolved at load. The host injects one GitHub App installation token per claim on the request config as `host_github_app_token: {token, scope_id}`; the collector spends it as the Bearer [REDACTED] that claim. A missing, blank, mis-shaped, or wrong-scope token object fails the claim with a matching `invalid_config` reason (see the table below).
- `github_app`: direct GitHub App auth. The three `github_app_*_env` entries name variables holding the App ID, installation ID, and private key, and `token_env` must be empty. The collector mints a fresh installation token itself for each claim; a failed mint is a distinct terminal failure, never an empty credential.

Token and key values never appear in facts, logs, status messages, or errors.

## Time and size limits

The claim deadline bounds the whole fetch: the window is read under it, and an overrun is released as retryable rather than stored as truth. Window sizes come from `max_runs`, `max_jobs`, `max_artifacts`, and `max_deployments` (defaults and caps in the table above), plus at most 20 statuses per deployment. Hitting a window cap is not a failure: what was fetched commits as `partial` with a warning such as `runs_truncated`. Lease and heartbeat settings load from the environment (`ESHU_CICD_RUN_CLAIM_LEASE_TTL`, default 60s; `ESHU_CICD_RUN_HEARTBEAT_INTERVAL`, default 20s) and the heartbeat must be shorter than the lease or startup fails.

## What operators see

The main signal is the claim result and its failure class. A claim this collector cannot serve ends terminal with `invalid_config.<reason>` — the bare bucket is never emitted — where the reason names the wiring fault an operator can act on:

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

Short-lived faults (transport errors, 5xx answers, malformed payloads, a throttle on the first request) instead release the claim `retryable` with the provider's suggested wait, clamped to between 30 seconds and 5 minutes. A throttle seen mid-window commits what was already fetched as `partial` with a `rate_limited` warning.

The collector writes one bounded diagnostic line per failed claim to standard error (stderr). The host keeps only the byte count, so that line helps only in local runs and tests. This repository emits no metrics: the manifest declares the prefix `eshu_dp_cicd_run_collector_`, but nothing is emitted under it. The `eshu_dp_ci_cd_run_*` signal names are preserved in `producer/telemetry.go` and their emission stays host-side.

## Keeping the same identity when you switch

If this collector replaces Eshu's built-in CI/CD run collector, `host.scope.id` must equal the old target's scope ID and `host.sourceSystem` must be `ci_cd_run`, or every stored fact key changes. The activation instance ID must instead be new: a duplicate collector instance ID stops the coordinator from starting. Only the `collector_instance_id` field inside the payload changes. Read the real values from the live built-in setup, never copy the placeholders in `config.example.yaml`.

One known gap: payloads carry the raw provider repository ID and leave the canonical `repository_id` unset, because the shared SDK does not yet export the ID helper. Eshu core treats that as unresolved (no joins) rather than an error, so the gap degrades to missing links, never wrong ones. Stable keys are otherwise identical to the built-in collector's; only `ci.pipeline_definition` keys differ until the helper lands.

## Permission to publish facts

All 9 fact kinds belong to Eshu core, so the host refuses them without a producer grant (written permission for this component to publish that kind). `grant.example.json` lists the 9 requests, each tied to this component and version and scoped to `ci_cd_run`. Eshu core issues, stores, and revokes grants; this repository only ships the request and never emits past host grant admission.

## Eshu dependencies

Exact versions from `go.mod`. Do not substitute another SDK line.

| Module | Version |
| --- | --- |
| Go | `1.26.10` |
| `github.com/eshu-hq/eshu-sdk/collector` | `v0.2.0-rc.1` |
| `github.com/eshu-hq/eshu-sdk/factschema` | `v0.1.0-rc.5` |
| `gopkg.in/yaml.v3` | `v3.0.1` |

The component ID is `dev.eshu.collector.cicd-run`, version `0.1.2`, collector kind `ci_cd_run`. It declares support for Eshu core `>=0.0.5 <0.2.0` and speaks `collector-sdk/v1alpha1` over the process adapter (see `manifest.yaml`). Check that range against your core version before enabling it; a mismatch makes the host refuse the component.

## What it never does

- It never starts, retries, cancels, or changes a pipeline, and it never stores anything itself — the host owns claim control, the commit boundary, and durable position state.
- It never issues or stores producer grants; grant issuance, storage, and revocation stay in Eshu core.
- It never emits `ci.workflow_image_evidence` (another collector owns it), and it never emits facts alongside a failure state.
- It never imports Eshu core internals, and it never puts tokens, private keys, credential-bearing web addresses, or provider payload text into facts, logs, or status messages.

## Build, test, and release

```bash
make ci     # lint + test + race; the full local check CI runs
make lint   # vet, golangci-lint, format check, tidy check, 500-line cap
make test   # build + full test suite
make race   # race suite + coverage floor
```

The test suite checks the 9-kind output against saved facts from the built-in collector (kinds, stable keys, and failure signals, including hostile inputs failing closed) and validates emitted results against the pinned SDK's own validator. Releases run from `.github/workflows/release.yml` on every push to `main` and on version tags. Nothing runs for a pull request, and the workflow deploys nothing. Tag `v<manifest version>` on the protected `main` head to publish: the workflow builds, pushes, signs, and attests `ghcr.io/eshu-hq/eshu-collector-cicd-run`; a tag on any other commit fails before anything is built. Copy the digest from the job summary into the activation's seed image.

## Docs and related projects

- `config.example.yaml` — full setup example with all three auth shapes. `manifest.yaml` — component identity, fact list, and core range. `grant.example.json` — the 9 grant requests. `producer/` — the fetch-plus-emission package.
- [Eshu core](https://github.com/eshu-hq/eshu) — the host that runs this collector.
- [eshu-sdk](https://github.com/eshu-hq/eshu-sdk) — shared SDK modules.
- [eshu-deploy](https://github.com/eshu-hq/eshu-deploy) — deployment examples and reference setups.

## Contributing

See `CONTRIBUTING.md`. In short: run `make ci` before pushing; it runs the same lint, test, and race checks as CI. Credentials travel by env reference, never inline, and address-cleaning tests must stay green.
