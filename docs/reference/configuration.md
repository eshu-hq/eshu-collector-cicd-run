# Configuration

Start by copying [config.example.yaml](../../config.example.yaml) from the repository root. It's the canonical example, with all three credential shapes shown, so this page explains the keys rather than repeating the file. If the two ever disagree, believe the example file and tell us.

The file has three parts. `host` names the job identity Eshu plans, `process` says how Eshu starts the binary, and `config.targets` holds this collector's provider targets. One activation covers the targets of one `ci_cd_run` instance, and each target keeps its own scope ID.

One quirk first, because it surprises people. At runtime this collector reads its activation from the process environment, not from the request: `ESHU_COLLECTOR_INSTANCES_JSON` holds the instance list, in the operator shape shared with the built-in collector. Only the per-claim host token (for `host_minted` targets) arrives on the request config. Lease and heartbeat settings also come from the environment: `ESHU_CICD_RUN_CLAIM_LEASE_TTL` (default 60s) and `ESHU_CICD_RUN_HEARTBEAT_INTERVAL` (default 20s). The heartbeat must be shorter than the lease or startup fails.

| Target key | Meaning |
| --- | --- |
| `provider` | `github_actions` (default when omitted) or `gitlab_ci` |
| `scope_id` | The claim scope this target serves; part of every stored fact key, so choose it once and keep it |
| `repository` | The `owner/repo` (GitHub) or project path (GitLab) to read |
| `auth_mode` | `token_env` (default), `host_minted`, or `github_app`. See Credentials below |
| `token_env` | For `token_env` targets: name of the environment variable holding the token |
| `github_app_id_env`, `github_app_installation_id_env`, `github_app_private_key_env` | For `github_app` targets only: names of the variables holding the App ID, installation ID, and private key |
| `allowed_repositories` | The only repositories this target may read |
| `api_base_url` | Provider API address, for example `https://api.github.com` |
| `max_runs`, `max_jobs`, `max_artifacts`, `max_deployments` | Window sizes. Defaults: 10 runs, 30 jobs, 30 artifacts, 10 deployments. Hard caps: 100, 500, 500, 100. `0` means "use the default". (`max_artifacts` is ignored for GitLab) |
| `source_uri` | Optional provenance address recorded on facts |

`host_token_file` was removed. Setting it in any mode fails at load, with guidance to use `host_minted` instead. Credential shapes never mix on one target. Mixed or unknown shapes fail at load, naming the env reference but never its value. Keep the request and result caps (64 MiB each) in step with the host's `process.stdout_limit_bytes` and `config.limits.maxResultBytes`, as the example shows.

## Credentials

Secrets never go in the config file. Each `github_actions` target picks exactly one shape with `auth_mode`, and `gitlab_ci` targets serve `token_env` only.

With `token_env` (the default), you use a static token. `token_env` names an environment variable the worker process already has, so `token_env: GITHUB_TOKEN` means "read the token from the variable called `GITHUB_TOKEN`." It's sent as `Authorization: Bearer`.

With `host_minted`, nothing is resolved at load. The host injects one GitHub App installation token per claim on the request config as `host_github_app_token: {token, scope_id}`, and the collector spends it as the Bearer token for that claim. A missing, blank, mis-shaped, or wrong-scope token object fails the claim with a matching `invalid_config` reason. The [guides](../guides/README.md) list each one.

With `github_app`, you get direct GitHub App auth. The three `github_app_*_env` entries name variables holding the App ID, installation ID, and private key, and `token_env` must be empty. The collector mints a fresh installation token itself for each claim. A failed mint is a distinct terminal failure, never an empty credential.

Token and key values never appear in facts, logs, status messages, or errors.

## Permission to publish facts

All 9 fact kinds belong to Eshu core, so the host refuses them without a producer grant (written permission for this component to publish that kind). [grant.example.json](../../grant.example.json) lists the 9 requests, each tied to this component and version and scoped to `ci_cd_run`. Eshu core issues, stores, and revokes grants. This repository only ships the request and never emits past host grant admission.
