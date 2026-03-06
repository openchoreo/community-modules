# Observability Logs Module for Moesif

This module collects container logs using Fluent Bit and exports them to Moesif via OpenTelemetry Collector.

## Prerequisites

- [OpenChoreo](https://github.com/openchoreo/openchoreo) must be installed with the **observability plane** enabled for this module to work.
- A Moesif account and Application ID from [Moesif](https://www.moesif.com/)

## Installation

Install this module in your OpenChoreo cluster using:

```bash
helm upgrade --install observability-logs-moesif \
  oci://ghcr.io/openchoreo/charts/observability-logs-moesif \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.1.0 \
  --set moesif.apps[0].env-name=default \
  --set moesif.apps[0].collector-application-id=YOUR_MOESIF_APPLICATION_ID
```

> **Note:** Replace `YOUR_MOESIF_APPLICATION_ID` with your actual Moesif Application ID. Alternatively, you can use `api-key` instead of `collector-application-id`.

### Configuration Options

You can configure multiple environments with different Moesif applications:

```bash
helm upgrade --install observability-logs-moesif \
  oci://ghcr.io/openchoreo/charts/observability-logs-moesif \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.1.0 \
  --set moesif.apps[0].env-name=development \
  --set moesif.apps[0].collector-application-id=YOUR_DEV_APP_ID \
  --set moesif.apps[1].env-name=production \
  --set moesif.apps[1].collector-application-id=YOUR_PROD_APP_ID
```

> **Important:** If multiple environments need to send logs to the same Moesif application, you must still define separate entries in `moesif.apps` for each environment, using the same `collector-application-id` or `api-key`:
>
> ```bash
> --set moesif.apps[0].env-name=development \
> --set moesif.apps[0].collector-application-id=YOUR_MOESIF_APP_ID \
> --set moesif.apps[1].env-name=staging \
> --set moesif.apps[1].collector-application-id=YOUR_MOESIF_APP_ID \
> --set moesif.apps[2].env-name=production \
> --set moesif.apps[2].collector-application-id=YOUR_MOESIF_APP_ID
> ```

### Using a values.yaml file

For easier configuration management, create a `moesif-values.yaml` file:

```yaml
# moesif-values.yaml
# Configuration for Moesif log collection

moesif:
  apps:
    # First Moesif application (e.g., development environment)
    - env-name: development                              # Environment name that matches openchoreo.dev/environment label
      collector-application-id: "YOUR_DEV_APP_ID"        # Moesif Application ID (use either this OR api-key)
      # api-key: "YOUR_DEV_API_KEY"                      # Alternative: Moesif API Key (use either this OR collector-application-id)
    
    # Second Moesif application (e.g., production environment)
    - env-name: production
      collector-application-id: "YOUR_PROD_APP_ID"
      # api-key: "YOUR_PROD_API_KEY"

    # Example: Multiple environments using the same Moesif application
    # Even if using the same Moesif app, you must define separate entries for each environment
    # - env-name: staging
    #   collector-application-id: "YOUR_DEV_APP_ID"      # Same as development
```

> **Note:** 
> - The `env-name` field should match the `openchoreo.dev/environment` label on your resources
> - Use either `collector-application-id` OR `api-key` (not both) - collector-application-id can be found in your Moesif `Apps and Team` settings
> - If multiple environments send logs to the same Moesif application, define separate entries for each environment with the same credentials

Then install with:

```bash
helm upgrade --install observability-logs-moesif \
  oci://ghcr.io/openchoreo/charts/observability-logs-moesif \
  --create-namespace \
  --namespace openchoreo-observability-plane \
  --version 0.1.0 \
  -f moesif-values.yaml
```
