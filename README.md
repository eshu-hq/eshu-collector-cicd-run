# eshu-collector-cicd-run

Extracted `ci_cd_run` producer for Eshu (#8854, epic #6707).

Ports the in-tree `collector-cicd-run` emission half per
[`SHIM_MAP.md`](https://github.com/eshu-hq/eshu/blob/main/go/cmd/collector-cicd-run/SHIM_MAP.md):
the `targetJSON` operator shape, provider-scope routing, the
`ci-cd-run-claim-` namespace, the nine emitted `ci.*` kinds at schema
`1.0.0`, deterministic generation-agnostic stable keys, URL/text
sanitizers, and the claim-outcome table
(complete / partial+warning / unchanged / retryable / terminal /
cancelled-no-result).

> Pre-release: producer extraction in progress. The in-tree collector
> remains the producer of record until cutover is proven (#4047 gate).

## Pins

Exact published SDK pair, no `replace` directives, no workspace:

```text
github.com/eshu-hq/eshu-sdk/collector v0.1.0-rc.3
github.com/eshu-hq/eshu-sdk/factschema v0.1.0-rc.5
```

Resolution verified through the public Go proxy
(`GOPROXY=proxy.golang.org go list -m <module>@<version>`); module-zip
checksums match
[`sdk-compatibility.md`](https://github.com/eshu-hq/eshu/blob/main/docs/public/extend/sdk-compatibility.md).

## Layout

```text
producer/            emission-half port (package producer)
  config.go          targetJSON operator shape, claim-capable instance
                     selection, credential-from-env-only, auth_mode shapes,
                     deployment-window bound, heartbeat guard
  auth.go            auth-mode normalization, fail-closed credential-shape
                     validation, provider status-class mapping (#9146)
  router.go          scope_id -> provider dispatch, claim-ID namespace
  kinds.go           nine-kind emission contract + SDK Contract
  emit.go            BuildResult outcome table + shared emission seam
  evidence.go        normalized provider-observation input (fixture seam)
  github.go          github_actions fact builders (8 of 9 kinds + pipeline)
  gitlab.go          gitlab_ci fact builders (run/job/artifact/warning)
  warnings.go        ci.warning builders + transport-level synthesis
  parity_golden_test.go
                     in-tree golden stable keys + payload spot vectors
  redact.go          URL/text sanitizers (stdlib-only)
  grant.go           fail-closed grant stub (DenyAllAuthorizer)
  urls.go            provider locator helpers
  telemetry.go       verbatim signal names (emission stays host-side)
testdata/8854-parity-cicd-run.json
                     parity census fixture, byte-identical to the in-tree
                     PREP fixture
```

## Authentication modes (`#9146` wiring port)

Each `github_actions` target declares exactly one credential shape via
`auth_mode` (`token_env` when omitted, `host_minted`, `github_app`).
Shapes never mix: `token_env` resolves a static Bearer token from its
env reference; `github_app` resolves the App ID, installation ID, and
private key from `github_app_*_env` references with `token_env` empty;
`host_minted` carries `host_token_file` with `token_env` empty. Any mix
or unknown mode fails closed at config load, naming the env reference
but never the value.

The satellite carries credential *shapes*: per-claim installation-token
minting (App) and per-claim host file resolution execute host-side where
the live provider is reachable, mirroring the in-tree
`ghactionsruntime` claim lifecycle. `max_deployments` (default 10, cap
100) carries the Deployments API window for the host fetch loop the
same way `max_runs` already does.

Provider-request status classes follow the in-tree contract:
direct-App 401/403/404 reads are `auth_error`, host-denied 401 refusals
are `auth_denied`, static-token failures keep `error`, throttling keeps
`rate_limited` (see `ClassifyProviderStatusCode`).

## Verification

```bash
GOPROXY=proxy.golang.org GOWORK=off go test -count=1 ./...
GOPROXY=proxy.golang.org GOWORK=off go vet ./...
```

The parity suite drives the ported producer from the checked-in fixture
and asserts kinds, stable-key determinism and generation-agnosticism,
claim/generation fencing, and every failure signal — including hostile
inputs failing closed. `parity_golden_test.go` pins every emitted
stable key byte-identical to the in-tree producer (cut from
`go/internal/collector/cicdrun` on the 8854 PREP branch), with
`ci.pipeline_definition` as the single documented expected-diverge
(uncomputable canonical `repository_id` until the SDK exports the
helper). Every emitted `complete`/`partial`/`retryable`/`terminal`
result additionally validates against the pinned SDK
`collector.Validator`, so the suite proves interop with the exact SDK
pair, not just self-consistency.

## Non-goals (core-owned, stay in `eshu-hq/eshu`)

- Grant issuance, storage, revocation (#6709): authorization here is a
  fail-closed stub. Nothing emits until core-issued grants land.
- Live provider fetch (GitHub REST / GitLab REST clients), claim control
  (lease, heartbeat, supervision), and the commit boundary (admission,
  projection, persistence, fencing).
- Durable watermark state (migration 078, #5429 gap detection).
- Telemetry emission (names preserved verbatim; emission stays host-side).

## Open gaps

P1 — no SDK-exported canonical repository-ID helper
(`repositoryidentity.CanonicalRepositoryID` is core-owned). Payloads
carry the raw `provider_repository_id` provenance and leave
`repository_id` unset; the reducer treats that as UNRESOLVED, never a
decode error, so this degrades to no false joins rather than wrong
joins. Stable keys are otherwise byte-identical to in-tree
(`identity.StableID` shares the exact algorithm); only
`ci.pipeline_definition` keys differ until the helper lands.

P1 — no SDK-exported opaque-credential URI predicate (the SDK keeps its
equivalent unexported in `validation.go`). The port uses a conservative
stdlib approximation with the SDK `Validator` as the fail-closed
backstop; a planted credentialed `source_ref` URI is rejected, proved by
test.
