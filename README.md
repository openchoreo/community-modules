# OpenChoreo Community Modules

Community modules are pluggable integrations that extend [OpenChoreo](https://openchoreo.dev/) platform capabilities. They allow operators to customize and enhance areas such as API gateways, CI workflows, observability, and GitOps, without being locked into a single tool stack.

## Prerequisites

- An installed and running [OpenChoreo](https://openchoreo.dev/) instance.

## Getting Started

Browse the available modules in the [OpenChoreo Ecosystem](https://openchoreo.dev/ecosystem/) and follow the installation instructions for each module.

For a deeper understanding of how modules work and how to add a new OpenChoreo module, see the [modules overview](https://openchoreo.dev/docs/platform-engineer-guide/modules/overview/) documentation.

Some modules bundle upstream Helm charts, listed under a **Dependencies** section in their README. Override any of their values with `--set <chart-name>.<value>=...` or by nesting them under `<chart-name>:` in your values file.

## Releases

Each module publishes its container image(s) to `ghcr.io/openchoreo/<image-name>` and its Helm chart to `oci://ghcr.io/openchoreo/helm-charts`. Releases are **author-driven**: PRs may merge without any version bump, and authors choose when to cut a release by bumping the module's `VERSION` file.

Each module with a Helm chart has a `VERSION` file at `<module>/VERSION`. Bumping it on `main` is what triggers the next release. The chart's `helm/Chart.yaml` always carries development placeholders (`version: 0.0.0-latest-dev`, `appVersion: "latest-dev"`) and should not be hand-edited — CI rewrites both at package time using the `VERSION` value.

To cut a release, edit `<module>/VERSION` to the new semver value (e.g. `0.2.1`) and merge the PR to `main`. On merge, CI publishes that single value as both the image tag and the chart `version`/`appVersion`:

- `ghcr.io/openchoreo/<image>:<VERSION>` for every image declared in the module's `module.yaml`.
- `<chart>:<VERSION>` with `appVersion=<VERSION>`.

Alongside any release, every merge to `main` that touches a file under `<module>/` also republishes development tags so consumers can pull the tip of `main`:

- `ghcr.io/openchoreo/<image>:latest-dev` and `ghcr.io/openchoreo/<image>:<short-sha>` (8-character commit SHA) for each image.
- `<chart>:0.0.0-latest-dev` with `appVersion=latest-dev`, and `<chart>:0.0.0-<short-sha>` with `appVersion=<short-sha>`.

Use the `latest-dev` tags for tracking `main`, the SHA-suffixed tags when you need a reproducible reference to a specific commit, and the `VERSION`-valued tags for released versions. The chart's `appVersion` always matches an image tag published in the same run.


## Reporting Issues

Please open issues in the main [openchoreo/openchoreo](https://github.com/openchoreo/openchoreo) repository.
