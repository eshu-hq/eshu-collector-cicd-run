# eshu-collector-cicd-run

Status: pre-release. Please don't rely on it in production yet.

Your pipelines run all day, and it's easy to lose track of what actually happened. This collector reads recent GitHub Actions or GitLab CI runs (jobs, steps, artifacts, deployments) and hands Eshu a checked summary of what ran and how it ended. It only reads history. Starting, cancelling, or changing a run is never on the table.

What you get:

- Recent workflow runs with their jobs and steps, from GitHub Actions or GitLab CI
- Artifacts and deployment outcomes, linked back to the run that produced them
- Failed builds included, with the failure recorded, so red pipelines don't vanish from the picture
- Windows cut short by a limit still commit what was fetched, flagged as partial with a warning
- Addresses and free text cleaned before anything leaves, credential-bearing ones dropped outright

```mermaid
flowchart LR
  A["GitHub Actions or GitLab CI"] --> B["This collector"] --> C["Eshu host"] --> D["Eshu core"]
```

Reach for it when Eshu needs current build and deploy activity for repositories you name, read on a schedule Eshu controls. Skip it to trigger pipelines, read run logs, or as a general GitHub or GitLab API tool.

## How Eshu runs it

The host starts the program for one claim at a time.
It sends one bounded `collector-sdk/v1alpha1` request on stdin and reads one result from stdout.
Unusually, the activation itself comes from the process environment (`ESHU_COLLECTOR_INSTANCES_JSON`), not from the request.
Then the host validates the result and commits it, and the collector stores nothing itself.

In other words, there's no long-running service to babysit here.
You enable it, Eshu calls it claim by claim, and each call either
comes back with checked facts or with a plain explanation of why not.
If that loop is new to you, the concepts page in the table below
walks through one claim from start to finish.

## Documentation

Start at the [documentation index](docs/README.md) if you'd like a map, or jump straight to what you need:

| Guide | What you will find |
| --- | --- |
| [Getting started](docs/getting-started/README.md) | The fastest honest path from a fresh checkout to something working |
| [Concepts](docs/concepts/README.md) | What it collects, how a claim plays out, and a worked example from the fixtures |
| [Guides](docs/guides/README.md) | Seeding, operating, and troubleshooting, plus how releases work |
| [Configuration reference](docs/reference/configuration.md) | Every config key, credentials, and the size limits |
| [Fact kinds reference](docs/reference/facts.md) | Every fact kind it can return, mirroring the manifest |
| [Architecture](docs/architecture/README.md) | Where it sits inside Eshu, what it's made of, and the versions it pins |
| [Diagrams](docs/diagrams/README.md) | System context, claim lifecycle, and deployment, each with a text version |
| [Examples](examples/README.md) | The real sample files in this repo and what each one is for |

## Related projects

- [Eshu core](https://github.com/eshu-hq/eshu) runs this collector and owns the final say on what's stored.
- [eshu-sdk](https://github.com/eshu-hq/eshu-sdk) holds the shared collector and fact-schema modules.
- [eshu-deploy](https://github.com/eshu-hq/eshu-deploy) has deployment examples you can copy from.

## Development

Run `make ci` and you're running the same lint, test, and race checks that CI runs.
The [contributing guide](docs/contributing/README.md) covers setup in a bit more detail,
including the tools you'll want installed first.

## Contributing, security, and license

Contributions are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md) first, and keep credentials as environment references, never inline values. If you spot a security issue, please don't file it as a public issue. Reach the maintainers privately so a fix can land before details circulate. Source files carry SPDX MIT license headers. See [CONTRIBUTING.md](CONTRIBUTING.md) for the project rules.
