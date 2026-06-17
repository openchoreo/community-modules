# OpenChoreo Community Modules

Community modules are pluggable integrations that extend [OpenChoreo](https://openchoreo.dev/) platform capabilities. They allow operators to customize and enhance areas such as API gateways, CI workflows, observability, and GitOps, without being locked into a single tool stack.

## Prerequisites

- An installed and running [OpenChoreo](https://openchoreo.dev/) instance.

## Getting Started

Browse the available modules in the [OpenChoreo Ecosystem](https://openchoreo.dev/ecosystem/) and follow the installation instructions for each module.

For a deeper understanding of how modules work and how to add a new OpenChoreo module, see the [modules overview](https://openchoreo.dev/docs/platform-engineer-guide/modules/overview/) documentation.

Some modules bundle upstream Helm charts, listed under a **Dependencies** section in their README. Override any of their values with `--set <chart-name>.<value>=...` or by nesting them under `<chart-name>:` in your values file.

## Releases

Each module publishes its container image(s) to `ghcr.io/openchoreo/<image-name>` and its Helm chart to `oci://ghcr.io/openchoreo/helm-charts`. Releases are **author-driven**: PRs may merge without any version bump, and authors choose when to cut a formal release by editing the module's `helm/Chart.yaml`.

### Tags published on every merge to `main`

If any file under `<module>/` changes in a merge, the CI workflow republishes both the container image(s) and the Helm chart with development tags so consumers can always pull the tip of `main`:

- **Container image(s)** declared in the module's `module.yaml`:
  - `ghcr.io/openchoreo/<image>:latest-dev` — moving tag, always points at the latest build from `main`.
  - `ghcr.io/openchoreo/<image>:<short-sha>` — immutable tag (8-character commit SHA) for pinning.
- **Helm chart**:
  - `<chart>:0.0.0-latest-dev` — moving version, with `appVersion=latest-dev`.
  - `<chart>:0.0.0-<short-sha>` — immutable version, with `appVersion=<short-sha>` matching the image tag.

Use the `latest-dev` tags for tracking `main`, and the SHA-suffixed tags when you need a reproducible reference to a specific commit. The chart's `appVersion` always matches an image tag published in the same run.

### Cutting a formal release

A formal release is triggered by editing `helm/Chart.yaml` in the module:

- Bump **`version`** to publish the Helm chart at that version (e.g. `<chart>:0.2.1`)
- Bump **`appVersion`** to publish the container image at that tag (e.g. `<image>:0.2.1`). 

Formal tags are published **in addition to** the development tags. Any release commit touches a file under `<module>/` (at minimum `Chart.yaml`), so the dev tags described above always fire alongside the formal tag(s).

### Manual re-publish (`workflow_dispatch`)

The `Build Images and Release Charts` workflow can be triggered manually from the Actions tab. A manual run re-publishes the **development tags only** (`latest-dev` and `<short-sha>` for every module with a chart) — it never publishes formal release tags, regardless of the current `Chart.yaml` values.

## Reporting Issues

Please open issues in the main [openchoreo/openchoreo](https://github.com/openchoreo/openchoreo) repository.
