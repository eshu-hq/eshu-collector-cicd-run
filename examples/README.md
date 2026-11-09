# Examples

No sample apps to run here. Instead, this page points at the real files you'd copy or read when setting this collector up. Each link is the canonical path in the repo, so nothing below can go stale.

- [config.example.yaml](../config.example.yaml) is the full activation example, showing all three credential shapes (`token_env`, `host_minted`, `github_app`). Copy it and replace the placeholders. [The configuration reference](../docs/reference/configuration.md) explains every key and links back to this file instead of repeating it.
- [grant.example.json](../grant.example.json) lists the 9 producer-grant requests, one per `ci.*` fact kind, each scoped to `ci_cd_run`. You'll need these recorded before the host will accept this collector's facts.
- [testdata/8854-parity-cicd-run.json](../testdata/8854-parity-cicd-run.json) is the parity fixture: one dual-provider instance (a GitHub target and a GitLab target) plus the expected outcome, including the nine emitted kinds and the one kind this producer must never emit. It doubles as a readable sketch of the instance JSON shape, and [concepts](../docs/concepts/README.md) uses it as the worked example.

Start with the example config if you're wiring things up, or with the parity fixture if you just want to see what a claim turns into.
