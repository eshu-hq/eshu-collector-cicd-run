# Contributing

Thanks for helping. The full rules live in [eshu-collector-cicd-run's CONTRIBUTING.md](../../CONTRIBUTING.md) at the repository root, and that file wins if anything here ever disagrees with it. This page is just the friendly on-ramp.

## Set yourself up

You'll need Go at the version pinned in `go.mod` (currently `1.26.11`) and `golangci-lint` v2.12.2. The root [CONTRIBUTING.md](../../CONTRIBUTING.md) shows the install command. Docker only matters if you want to exercise the image lane locally.

## Check your work

One command runs the whole local contract:

```bash
make ci   # lint + test + race, the same checks CI runs
```

Useful pieces on their own: `make lint`, `make test`, and `make race` (which also enforces the coverage floor). The suite checks the 9-kind output against saved facts from the built-in collector, including hostile inputs failing closed, and validates results against the pinned SDK's own validator.

Please run it before you push. The test suite is the product contract here, so if a test is in your way, that usually means the change needs rethinking rather than the test needing weakening.

## House rules worth knowing

Credentials always travel by environment reference, never as inline values, and they never get logged. Keep Go files under the 500-line cap the lint lane enforces. And if you touch the fact list, update [the facts reference](../reference/facts.md) and `manifest.yaml` together, since one mirrors the other.
