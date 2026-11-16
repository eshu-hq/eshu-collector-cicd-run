# Concepts

This page explains what this collector is for and how it behaves, in plain words. If you want the pictures, they're all on the [diagrams page](../diagrams/README.md), and section headings below link to the matching one.

## The short story

Eshu is the core system. A collector is a small helper program: it reads one outside source, turns what it finds into facts (small, typed records), and gives those facts back to Eshu. The [system context diagram](../diagrams/README.md#system-context) shows where this one sits.

CI/CD is the automated pipeline that builds, tests, and deploys code. A run is one execution of that pipeline, made of jobs, and jobs are made of steps. A claim is one unit of work Eshu sends the collector, and an activation is one configured copy of it, set up with one or more provider targets.

One claim covers one repository target. For that target the collector itself fetches a bounded recent window from the provider. No host fetch loop feeds it. From GitHub Actions that's recent workflow runs with their jobs, steps, and artifacts, plus deployments and their statuses. From GitLab CI it's recent pipelines with their jobs and artifacts. Window sizes come from config (`max_runs`, `max_jobs`, and friends, in [the configuration reference](../reference/configuration.md)), plus at most 20 statuses per deployment.

Hitting a window cap isn't a failure. What was fetched commits as `partial` with a warning such as `runs_truncated`, and a throttle seen mid-window does the same with a `rate_limited` warning. The claim deadline bounds the whole fetch, and an overrun is released as retryable rather than being stored as truth. Short-lived faults (transport errors, 5xx answers, malformed payloads, a throttle on the very first request) also release the claim as retryable, with the provider's suggested wait clamped to between 30 seconds and 5 minutes.

If you're switching from Eshu's built-in CI/CD run collector, keep the identity steady: `host.scope.id` must equal the old target's scope ID and `host.sourceSystem` must be `ci_cd_run`, or every stored fact key changes. The activation instance ID must instead be new, because a duplicate stops the coordinator from starting. Only the `collector_instance_id` field inside the payload changes. One known gap to be aware of: payloads carry the raw provider repository ID and leave the canonical `repository_id` unset, since the shared SDK doesn't yet export the ID helper. Core treats that as unresolved, so the gap costs you some joins but never produces wrong ones. Stable keys are otherwise identical to the built-in collector's. Only `ci.pipeline_definition` keys differ until the helper lands.

## How a run can end

A claim ends as `complete`, `partial` with a warning (a limit or a mid-window throttle cut the window short, but what was fetched is committed), `unchanged`, `retryable` (try again later), or `terminal` (stop, a person must fix something first). A cancelled claim returns no result at all. Requests and results are each capped at 64 MiB.

One distinction here is easy to get wrong, so let's be precise. A terminally failed claim returns no facts, and neither does a retryable, cancelled, or unchanged one. But a provider run whose own conclusion is failure still emits facts. A red build produces `ci.run`, `ci.job`, and `ci.step` facts exactly like a green one, with the failure recorded in each payload's `result` field. Failed builds are data. Only failed claims are empty.

## The facts, in one breath

This collector returns its facts at schema `1.0.0` with confidence `reported`, which means the source said so and Eshu hasn't checked it directly. Every fact carries where it came from: source system, scope, generation, a stable key, a cleaned source address, and a source record ID. The full list lives in [the fact kinds reference](../reference/facts.md), which mirrors `manifest.yaml`.

## A worked example

The repo pins its expected shape in [testdata/8854-parity-cicd-run.json](../../testdata/8854-parity-cicd-run.json), a parity fixture describing one dual-provider instance. Its GitHub target serves the scope `ci-cd:github-actions:example-org/example-repo` for the repository `example-org/example-repo`, with a deliberately tiny window: `max_runs: 1`, `max_jobs: 25`, `max_artifacts: 25`. A GitLab target for `eshu-hq/demo` sits beside it.

So a claim for that GitHub scope sends the collector off to fetch the single most recent run and its jobs, steps, and artifacts. What comes back, per the fixture's `expected` block, is drawn from the nine `ci.*` kinds at schema `1.0.0`: pipeline definition, run, job, step, artifact, trigger edge, environment observation, deployment event, and warning. And if that most recent run failed on GitHub's side, the facts still come back. The run, job, and step payloads simply carry `result` set to the provider's failure conclusion, which you can confirm in `producer/github.go`, where each payload's `result` is filled from the provider's conclusion, and in `producer/emit.go`, where only terminal, retryable, cancelled, or unchanged claim states short-circuit before facts are built. The fixture also records the one kind this producer must never emit, `ci.workflow_image_evidence`, under `excluded_kinds`.

## What it never does

It never starts, retries, cancels, or changes a pipeline, and it never stores anything itself. The host owns claim control, the commit boundary, and durable position state. It never issues or stores producer grants, since issuance, storage, and revocation stay in Eshu core. And it never emits facts alongside a failure state. A terminally failed claim returns no facts, full stop.

That last point is about claims, not about your builds. A provider run whose own conclusion is failure still emits its `ci.run`, `ci.job`, and `ci.step` facts with the outcome recorded in the payload. It never imports Eshu core internals, and it never puts tokens, private keys, credential-bearing web addresses, or provider payload text into facts, logs, or status messages.
