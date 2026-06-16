# OpenChoreo Community Modules

Community modules are pluggable integrations that extend [OpenChoreo](https://openchoreo.dev/) platform capabilities. They allow operators to customize and enhance areas such as API gateways, CI workflows, observability, and GitOps, without being locked into a single tool stack.

## Prerequisites

- An installed and running [OpenChoreo](https://openchoreo.dev/) instance.

## Getting Started

Browse the available modules in the [OpenChoreo Ecosystem](https://openchoreo.dev/ecosystem/) and follow the installation instructions for each module.

For a deeper understanding of how modules work and how to add a new OpenChoreo module, see the [modules overview](https://openchoreo.dev/docs/platform-engineer-guide/modules/overview/) documentation.

## Releases

Each module publishes its container image(s) to `ghcr.io/openchoreo/<image-name>` and its Helm chart to `oci://ghcr.io/openchoreo/helm-charts`. Releases are **author-driven**: PRs may merge without any version bump, and authors choose when to cut a formal release by editing the module's `helm/Chart.yaml`.

### Tags published on every merge to `main`

For each module touched by a merge, the CI workflow publishes development artifacts so consumers can always pull the tip of `main`:

- **Container image** — published when any file *outside* `<module>/helm/` changes ( sources, `Dockerfile`, `Makefile`, `module.yaml`, `init/`, etc.):
  - `ghcr.io/openchoreo/<image>:latest-dev` — moving tag, always points at the latest build from `main`.
  - `ghcr.io/openchoreo/<image>:<short-sha>` — immutable tag (8-character commit SHA) for pinning.
- **Helm chart** — published when any file under `<module>/helm/` changes:
  - `<chart>:0.0.0-latest-dev` — moving version.
  - `<chart>:0.0.0-<short-sha>` — immutable version.

Use the `latest-dev` tags for tracking `main`, and the SHA-suffixed tags when you need a reproducible reference to a specific commit.

### Cutting a formal release

A formal release is triggered by editing `helm/Chart.yaml` in the module:

- Bump **`version`** to publish the Helm chart at that version (e.g. `<chart>:0.2.1`)
- Bump **`appVersion`** to publish the container image at that tag (e.g. `<image>:0.2.1`). 

Formal tags are published **independently** of the development tags, on the same per-artifact gating described above. In practice:

- A bump to `appVersion` alone publishes only `<image>:<appVersion>` — the image's `:latest-dev` and `:<short-sha>` tags do *not* fire because no file outside `helm/` changed. The chart's dev tags (`0.0.0-latest-dev`, `0.0.0-<short-sha>`) *do* fire, since `Chart.yaml` lives under `helm/`.
- A bump to `version` alone publishes the formal chart `<chart>:<version>` and the chart's dev tags. No image tags are published.
- A release commit that also touches code outside `helm/` (Go sources, `Dockerfile`, etc.) additionally publishes the image's `:latest-dev` and `:<short-sha>` tags.

### Manual re-publish (`workflow_dispatch`)

The `Build Images and Release Charts` workflow can be triggered manually from the Actions tab. A manual run re-publishes the **development tags only** (`latest-dev` and `<short-sha>` for every module with a chart) — it never publishes formal release tags, regardless of the current `Chart.yaml` values.

## Reporting Issues

Please open issues in the main [openchoreo/openchoreo](https://github.com/openchoreo/openchoreo) repository.
