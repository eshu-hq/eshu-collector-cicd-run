# Fact kinds

This table mirrors the `emittedFacts` list in [manifest.yaml](../../manifest.yaml). "Mirrors" matters: the manifest is the source of truth, and this page explains it in friendlier words. If you change one, update the other in the same change so they never drift apart.

Every kind below sits at schema `1.0.0` with confidence `reported`, meaning the source said so and Eshu hasn't checked it directly. Every fact carries where it came from: source system, scope, generation, a stable key, a cleaned source address, and a source record ID.

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

GitHub targets can produce all nine kinds. GitLab targets produce the run, job, artifact, and warning kinds. Web addresses and free text are cleaned before anything leaves the collector, and credential-bearing addresses are dropped outright. This collector never emits `ci.workflow_image_evidence`. That kind belongs to another collector.

All of these kinds belong to Eshu core, so the host refuses them without a producer grant (written permission for this component to publish that kind). Eshu core issues grants. This repository mints none.

For the story behind the table, including a worked example from the test fixtures, see [concepts](../concepts/README.md).
