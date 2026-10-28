# eshu-collector-cicd-run

Extracted `ci_cd_run` producer for Eshu (#8854, epic #6707).

Ports the in-tree `collector-cicd-run` fetch-plus-emission halves per
[`SHIM_MAP.md`](https://github.com/eshu-hq/eshu/blob/main/go/cmd/collector-cicd-run/SHIM_MAP.md):
the `targetJSON` operator shape, provider-scope routing, the
`ci-cd-run-claim-` namespace, the nine emitted `ci.*` kinds at schema
`1.0.0`, deterministic generation-agnostic stable keys, URL/text
sanitizers, the bounded provider fetch (GitHub Actions plus GitLab CI
windows, caps, and outcome signals), and the claim-outcome table
(complete / partial+warning / unchanged / retryable / terminal /
cancelled-no-result).

Fetch moved into the satellite per the arbiter ruling on
[eshu-hq/eshu#9337](https://github.com/eshu-hq/eshu/issues/9337): each
claim fetches its own bounded provider window under the claim deadline
and normalizes into the `producer.Evidence` seam, whose builders are
unchanged. No host fetch loop feeds this collector.

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
./                  satellite root (package collector)
  contract.go        component identity, emitted kinds, SDK contract
  handle.go          one-claim request handler (bind, credential, fetch,
                     compose with invalid_config.<reason> terminals)
  manifest.yaml      component manifest (embedded, seeded by the binary)
  grant.example.json producer-grant request, one per emitted kind
  config.example.yaml
                     activation example (targetJSON operator shape)
cmd/collector/     satellite binary (claim stdio + seed installer)
Dockerfile           multi-stage build to distroless static
producer/            fetch-plus-emission port (package producer)
  config.go          targetJSON operator shape, claim-capable instance
                     selection, credential-from-env-only, auth_mode shapes,
                     fetch-window bounds, heartbeat guard
  auth.go            auth-mode normalization, fail-closed credential-shape
                     validation, provider status-class mapping (#9146)
  fetch.go           claim credential seam (host token object), terminal /
                     retryable outcome errors, shared HTTP plumbing
  fetch_github.go    github_actions window fetch (runs/jobs/artifacts/
                     deployments windows, truncation + throttle signals)
  fetch_gitlab.go    gitlab_ci window fetch (pipelines/detail/jobs,
                     X-Total signals)
  normalize_github.go
                     raw GitHub maps into the Evidence seam
  appauth.go         in-satellite direct-App installation-token mint
                     (stdlib RS256 JWT, no key material in errors)
  window.go          Window composition into the claim-outcome table
  router.go          scope_id -> provider dispatch, claim-ID namespace
  kinds.go           nine-kind emission contract + SDK Contract
  emit.go            BuildResult outcome table + shared emission seam
  evidence.go        normalized provider-observation seam (fetch output)
  github.go          github_actions fact builders (8 of 9 kinds + pipeline)
  gitlab.go          gitlab_ci fact builders (run/job/artifact/warning)
  warnings.go        ci.warning builders + transport-level synthesis
  parity_golden_test.go
                     in-tree golden stable keys + payload spot vectors
  redact.go          URL/text sanitizers (stdlib-only)
  grant.go           fail-closed grant stub (DenyAllAuthorizer),
                     claim-target binding, App mint entrypoint
  urls.go            provider locator helpers
  telemetry.go       verbatim signal names (emission stays host-side)
testdata/8854-parity-cicd-run.json
                     parity census fixture, byte-identical to the in-tree
                     PREP fixture
```

## Authentication modes (`#9146` wiring port, fetch in `#9337`)

Each `github_actions` target declares exactly one credential shape via
`auth_mode` (`token_env` when omitted, `host_minted`, `github_app`).
Shapes never mix: `token_env` resolves a static Bearer token from its
env reference; `github_app` resolves the App ID, installation ID, and
private key from `github_app_*_env` references with `token_env` empty;
`host_minted` resolves nothing at load — the host injects one
installation token per claim on the request config as
`host_github_app_token: {token, scope_id}`, spent as the Bearer. Any
mix or unknown mode fails closed at config load, naming the env
reference but never the value. `host_token_file` was removed: any
configured value fails closed with removal guidance, in every mode.
`gitlab_ci` targets serve `token_env` only.

The satellite spends every credential shape itself: static tokens read
at load, host tokens spent per claim from the request config, direct
App triples minted in-satellite per claim with a stdlib RS256 JWT (no
JWT dependency, no key material in errors). Fetch bounds carry over
from the in-tree runtime: `max_runs` (default 10, cap 100),
`max_jobs`/`max_artifacts` (provider-page default 30, cap 500),
`max_deployments` (default 10, cap 100), per-deployment statuses (20).
The claim deadline bounds the whole fetch.

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

## Satellite binary and image

`cmd/collector` serves one claim over SDK stdio and installs itself:

```bash
go build ./...
./collector --sdk-stdio < request.json > result.json
./collector seed --dest /component --image-ref ghcr.io/eshu-hq/eshu-collector-cicd-run@sha256:<digest>
```

The claim handler loads the activation from the environment
(`ESHU_COLLECTOR_INSTANCES_JSON` plus the credential env references its
targets name), binds the claim to its provider target, resolves the
claim credential, fetches the bounded provider window under the claim
deadline, and composes the result through `producer.BuildWindowResult`.
Process mode relies on host grant admission; claim binding (kind,
instance, scope, generation, fencing) is still enforced per emission.
A claim the satellite cannot serve terminal-fails with an
`invalid_config.<reason>` class (see the table below); a transient
fault releases the claim retryable.

The image is a static binary on distroless (no shell, no package
manager), mirroring `eshu-collector-jira`:

```bash
docker build -t ghcr.io/eshu-hq/eshu-collector-cicd-run:v0.1.2 .
docker run --rm --network none -v /tmp/seed:/component \
  ghcr.io/eshu-hq/eshu-collector-cicd-run:v0.1.2 seed --dest /component
```

## Releasing

Push a `v*` tag and the `release` workflow builds, pushes, signs, and
attests the image; the tag must match `manifest.yaml` metadata.version
(`v0.1.2`; `v0.1.1` predates the fork-ii fetch port, `v0.1.0` shipped the pre-#9329 manifest). Copy the digest from the job summary into the
activation's `seed.image`. Nothing in the release workflow deploys.

## Terminal failure classes

A claim the satellite cannot serve terminal-fails with
`invalid_config.<reason>` — the bare bucket is never emitted. Reasons
are bounded vocabulary an operator can act on; classes, errors, and log
lines never carry tokens, credentials, or provider payload text.

| Class | Meaning |
|---|---|
| `invalid_config.activation` | Unloadable activation or unknown provider/mode |
| `invalid_config.claim_binding` | Claim kind/instance/generation/fencing mismatch |
| `invalid_config.target_scope_mismatch` | Claim scope has no registered target |
| `invalid_config.missing_host_token` | `host_minted` claim without the request token object |
| `invalid_config.empty_host_token` | Request token object with a blank token |
| `invalid_config.host_token_shape` | Request token is not the `{token, scope_id}` object |
| `invalid_config.host_token_scope_mismatch` | Token `scope_id` differs from the target scope |
| `invalid_config.github_app_mint_failed` | Direct-App mint refused (distinct from host faults) |
| `invalid_config.provider_denied` | Provider 401/403: credential refused at claim scope |
| `invalid_config.provider_not_found` | Provider 404: repository/run/pipeline missing |
| `invalid_config.provider_rejected` | Any other deterministic provider refusal |

Transient faults (transport, 5xx, malformed payload, first-request
throttle) release the claim `retryable` (`transient_transport`) with
bounded provider retry guidance instead. A throttle observed mid-window
commits the fetched prefix `partial` with a `rate_limited` warning, and
a `max_runs` ceiling cut commits `partial` with a `runs_truncated`
warning — per the SHIM_MAP outcome table.

## Non-goals (core-owned, stay in `eshu-hq/eshu`)

- Grant issuance, storage, revocation (#6709): authorization here is a
  fail-closed stub. Nothing emits until core-issued grants land.
- Claim control (lease, heartbeat, supervision) and the commit boundary
  (admission, projection, persistence, fencing).
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
