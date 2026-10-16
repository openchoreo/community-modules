# Configure identities for Agent Manager v1.0.0

Use this procedure to add Agent Manager identities to an existing ThunderID 1.0.x installation. It creates AMP-specific clients, resource servers, roles, and an administrators group. It preserves OpenChoreo clients, users, signing keys, and Thunder's built-in Administrator role. The only shared settings it changes are the two in [step 5](#5-configure-shared-settings), and only after you confirm them.

Run the commands from the folder where you downloaded the [module tools](README.md#download-the-module-tools). Python 3.10+ and access to Thunder's administration API are required. This is a fresh provisioning procedure: it refuses to overwrite an existing AMP resource. For existing registrations, inventory and reconcile their IDs, permissions, and credentials separately.

## Compatibility

The procedure uses the ThunderID 1.0 import and server-configuration APIs, so it depends on the Thunder version your OpenChoreo installation runs:

| OpenChoreo version | Bundled Thunder | Supported |
|---|---|---|
| v1.3.x | ThunderID `1.0.1` (`oci://ghcr.io/thunder-id/helm-charts/thunderid`) | Yes |
| v1.2.x | Thunder `0.28.0` (`oci://ghcr.io/asgardeo/helm-charts/thunder`) | No. Upgrade Thunder to ThunderID 1.0.x first, through a separately validated path |

Check the running version before you start:

```bash
helm list -A | grep -iE 'thunder'
```

The tool was verified against the ThunderID v1.0.0 API definitions. The upstream Agent Manager v1.0.0 guide installs its own ThunderID 1.0.0 on OpenChoreo 1.2.0. This module runs AMP v1.0.0 against the Thunder bundled with OpenChoreo v1.3.x instead, and that setup has been validated end to end on a local k3d cluster: sign-in, agent builds and deployments, MCP access with API keys and OAuth, traces, monitors, and an additional environment.

## 1. Collect the existing identity configuration

Resolve these values from Thunder's administration console or API:

| Value | Where it is used |
|---|---|
| Actual UUID of the `default` organization unit | All AMP identities; do not copy a UUID from a different installation |
| Existing login flow ID | Console, CLI, and MCP login |
| Existing user types, plus `engineer` | Interactive clients' `allowedUserTypes`. OpenChoreo's reference setup uses `openchoreo-user`. Keep `engineer` in the list: the AMP console creates users with that type, and the tool refuses a configuration without it |
| IDs of users who should administer AMP | Membership of the new AMP administrators group |
| Existing System resource server ID | AMP's separate system-access role |
| Thunder public issuer URL | JWT validation and System resource identifier |
| AMP console, API, observer, and instrumentation URLs | Redirects, routing, MCP resource identifiers, and agent instrumentation |
| Reachable token, JWKS, and OpenChoreo API endpoints | Service-to-service calls |

The documented AMP setup uses the `default` organization. Its UUID may differ between installations. Users selected as AMP administrators must belong to that organization and have a user type allowed by the interactive clients. Preserve their existing accounts and OpenChoreo permissions.

Thunder's System resource identifier must be `<thunder-public-url>/mcp`, matching the resource parameter AMP's system client sends. The provisioning tool checks this but does not change it.

Back up existing identity configuration and keep the same issuer and signing keys. The tool needs a system-scoped administrative token in `THUNDER_ADMIN_TOKEN`. Do not place tokens in Git or command-line arguments.

OpenChoreo v1.3.x registers an `openchoreo-system-app` client for this purpose: it uses client credentials and holds the `system` permission on the System resource server. Request a token from it with the System resource identifier as the `resource`. Set `THUNDER_PUBLIC_URL` to the issuer.

The client and its secret belong to OpenChoreo, not to Agent Manager: whoever installed OpenChoreo set the secret in the Thunder Helm values (the `55-system-app.yaml` bootstrap file). Helm keeps those values in the cluster, so read the secret from the Thunder release. This does not print it:

```bash
export OPENCHOREO_SYSTEM_APP_SECRET="$(helm get values thunder -n "${THUNDER_NAMESPACE}" -a -o json | python3 -c '
import json, re, sys
scripts = (json.load(sys.stdin).get("bootstrap") or {}).get("scripts") or {}
for text in scripts.values():
    m = re.search(r"clientId:\s*openchoreo-system-app\s*\n\s*clientSecret:\s*\"?([^\s\"]+)", text)
    if m:
        print(m.group(1)); break
else:
    sys.exit("openchoreo-system-app is not in the Thunder bootstrap values; set OPENCHOREO_SYSTEM_APP_SECRET by hand")')"
```

If your Thunder was installed without the bootstrap files, ask whoever installed OpenChoreo for the secret and export it instead. The reference installation uses `openchoreo-system-app-secret`. An empty or wrong value makes the token request below fail with HTTP 401.

```bash
export THUNDER_ADMIN_TOKEN=$(curl -fsS -X POST "${THUNDER_PUBLIC_URL}/oauth2/token" \
  -d grant_type=client_credentials -d client_id=openchoreo-system-app \
  -d client_secret="${OPENCHOREO_SYSTEM_APP_SECRET}" -d scope=system \
  --data-urlencode "resource=${THUNDER_PUBLIC_URL}/mcp" \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["access_token"])')
```

The token is valid for one hour. Request a new one if a later command reports HTTP 401.

Look up the organization unit and the System resource server, and list the existing interactive clients with their login flows and allowed user types, and the existing users:

```bash
thunder_get() { curl -fsS -H "Authorization: Bearer ${THUNDER_ADMIN_TOKEN}" "${THUNDER_PUBLIC_URL}$1"; }

export AMP_OU_ID="$(thunder_get /organization-units/tree/default \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')"
export AMP_SYSTEM_RS_ID="$(thunder_get '/resource-servers?limit=100' \
  | python3 -c 'import json,sys; print(next(r["id"] for r in json.load(sys.stdin)["resourceServers"] if r["name"] == "System"))')"
echo "AMP_OU_ID=${AMP_OU_ID}  AMP_SYSTEM_RS_ID=${AMP_SYSTEM_RS_ID}"

echo "Interactive clients (name, login flow ID, allowed user types):"
thunder_get '/applications?limit=100' | python3 -c 'import json,sys; [print(a["id"]) for a in json.load(sys.stdin)["applications"]]' \
  | while read -r id; do
      thunder_get "/applications/${id}" | python3 -c 'import json,sys; a=json.load(sys.stdin); a.get("authFlowId") and print(" ", a["name"], "|", a["authFlowId"], "|", a.get("allowedUserTypes"))'
    done

echo "Users (ID, user type, username):"
thunder_get '/users?limit=100' | python3 -c 'import json,sys; [print(" ", u["id"], "|", u.get("type"), "|", (u.get("attributes") or {}).get("username")) for u in json.load(sys.stdin)["users"]]'
```

Then choose two things from the listings, and let the next block turn them into IDs:

- `AMP_CONSOLE_CLIENT_NAME`: the client OpenChoreo's console signs in with. On OpenChoreo's reference installation, that is `Backstage`. Its login flow and allowed user types become AMP's.
- `AMP_ADMIN_USERNAMES`: the usernames of the users who should administer AMP, separated by spaces. On the reference installation, `admin@openchoreo.dev` is an existing administrator.

The block exports `AMP_AUTH_FLOW_ID`, `AMP_ALLOWED_USER_TYPES`, and `AMP_ADMIN_USER_IDS`, and prints them. It stops with a message if the client or a username does not exist; correct the name and run it again:

```bash
export AMP_CONSOLE_CLIENT_NAME="Backstage"
export AMP_ADMIN_USERNAMES="admin@openchoreo.dev"

AMP_CONSOLE_CLIENT_ID="$(thunder_get '/applications?limit=100' | python3 -c '
import json, os, sys
name = os.environ["AMP_CONSOLE_CLIENT_NAME"]
ids = [a["id"] for a in json.load(sys.stdin)["applications"] if a["name"] == name]
print(ids[0]) if ids else sys.exit(f"No Thunder client is named {name}; set AMP_CONSOLE_CLIENT_NAME from the listing above.")')"
eval "$(thunder_get "/applications/${AMP_CONSOLE_CLIENT_ID}" | python3 -c '
import json, shlex, sys
app = json.load(sys.stdin)
print("export AMP_AUTH_FLOW_ID=" + shlex.quote(app["authFlowId"]))
print("export AMP_ALLOWED_USER_TYPES=" + shlex.quote(" ".join(app.get("allowedUserTypes") or [])))')"
eval "$(thunder_get '/users?limit=100' | python3 -c '
import json, os, shlex, sys
wanted = os.environ["AMP_ADMIN_USERNAMES"].replace(",", " ").split()
users = {(u.get("attributes") or {}).get("username"): u["id"] for u in json.load(sys.stdin)["users"]}
missing = [name for name in wanted if name not in users]
if missing:
    sys.exit("No Thunder user is named " + ", ".join(missing) + "; set AMP_ADMIN_USERNAMES from the listing above.")
print("export AMP_ADMIN_USER_IDS=" + shlex.quote(" ".join(users[name] for name in wanted)))')"
echo "AMP_AUTH_FLOW_ID=${AMP_AUTH_FLOW_ID}  AMP_ALLOWED_USER_TYPES=${AMP_ALLOWED_USER_TYPES}  AMP_ADMIN_USER_IDS=${AMP_ADMIN_USER_IDS}"
```

## 2. Prepare the configuration and client secrets

Create a virtual environment in that folder, and a private working directory outside it:

```bash
python3 -m venv .venv
.venv/bin/python -m pip install -r scripts/requirements.txt

umask 077
AMP_IDENTITY_WORK="$(mktemp -d)"
cp values/thunder-identities.yaml "${AMP_IDENTITY_WORK}/configuration.yaml"
```

Write `configuration.yaml` from the values looked up in [step 1](#1-collect-the-existing-identity-configuration) and the README's Configuration Variables. The command writes nothing and names the missing variables if any is unset:

```bash
python3 - "${AMP_IDENTITY_WORK}/configuration.yaml" <<'EOF'
import json, os, sys
env = os.environ.get
required = ["AMP_OU_ID", "AMP_AUTH_FLOW_ID", "AMP_ADMIN_USER_IDS", "AMP_SYSTEM_RS_ID",
            "THUNDER_PUBLIC_URL", "AMP_CONSOLE_URL", "AMP_API_URL", "AMP_OBSERVER_URL",
            "INSTRUMENTATION_URL", "THUNDER_INTERNAL_URL", "THUNDER_INTERNAL_HOST",
            "THUNDER_PORT", "OPENCHOREO_API_URL"]
missing = [name for name in required if not env(name)]
if missing:
    sys.exit("Nothing written. Export first: " + ", ".join(missing))
user_types = (env("AMP_ALLOWED_USER_TYPES") or "openchoreo-user").replace(",", " ").split()
if "engineer" not in user_types:
    user_types.append("engineer")
config = {
    "organizationUnitId": env("AMP_OU_ID"),
    "organizationUnitHandle": "default",
    "authFlowId": env("AMP_AUTH_FLOW_ID"),
    "allowedUserTypes": user_types,
    "adminUserIds": env("AMP_ADMIN_USER_IDS").replace(",", " ").split(),
    "systemResourceServerId": env("AMP_SYSTEM_RS_ID"),
    "thunderPublicUrl": env("THUNDER_PUBLIC_URL"),
    "consolePublicUrl": env("AMP_CONSOLE_URL"),
    "apiPublicUrl": env("AMP_API_URL"),
    "observerPublicUrl": env("AMP_OBSERVER_URL"),
    "instrumentationUrl": env("INSTRUMENTATION_URL"),
    "thunderTokenUrl": env("THUNDER_INTERNAL_URL") + "/oauth2/token",
    "thunderJwksUrl": env("THUNDER_INTERNAL_URL") + "/oauth2/jwks",
    "thunderResolveToHost": env("THUNDER_INTERNAL_HOST") + ":" + env("THUNDER_PORT"),
    "openChoreoApiUrl": env("OPENCHOREO_API_URL"),
}
with open(sys.argv[1], "w") as stream:
    stream.writelines(f"{key}: {json.dumps(value)}\n" for key, value in config.items())
print("Wrote " + sys.argv[1])
EOF
cat "${AMP_IDENTITY_WORK}/configuration.yaml"
```

Review the output. To edit the file by hand instead, use the comments in the copied `configuration.yaml` as a guide. Public URLs are origins without path prefixes; specify the exact scheme and port. The renderer rejects unfilled ID placeholders and example URLs.

Create new client secrets once, or load previously generated values from your secret manager:

```bash
export AMP_API_CLIENT_SECRET="$(openssl rand -hex 32)"
export AMP_SYSTEM_CLIENT_SECRET="$(openssl rand -hex 32)"
export AMP_PUBLISHER_CLIENT_SECRET="$(openssl rand -hex 32)"
export AM_OBSERVER_CLIENT_SECRET="$(openssl rand -hex 32)"
```

Store these values securely and reuse them in the corresponding services. Re-running the generation commands produces different credentials; do not do that after importing the clients.

## 3. Render the bundle and matching Helm values

```bash
.venv/bin/python scripts/provision-thunder-identities.py render \
  --config "${AMP_IDENTITY_WORK}/configuration.yaml" \
  --output-dir "${AMP_IDENTITY_WORK}/rendered"
```

The renderer creates a private directory containing:

| File | Purpose |
|---|---|
| `identities.yaml` | Rendered Thunder import documents; **not** a Kubernetes manifest |
| `agent-manager.yaml` | Core chart identity and routing overrides for v1.0.0 |
| `deployment.json` | Non-secret deployment inputs for preflight and readiness checks |
| `extension-credentials.json` | Evaluation-publisher and observer credentials for the extension configuration |

The directory is mode `0700`, and its files are mode `0600`. Existing output directories are rejected. These files contain credentials: keep them outside Git and transfer them to your normal secret-management system before removing the working directory.

The bundle includes eight clients, three resource servers, four AMP roles, one administrators group, one AMP-owned Thunder system-access role, and the `engineer` user type. It includes the complete release permission catalog. CLI and MCP clients are included so those entry points can use the same identity configuration.

The AMP console creates users with the `engineer` user type, so the bundle creates it in the AMP organization with Agent Manager's schema; without it, **Create User** fails with `Failed to create user` (Thunder `USR-1021 User type not found`). It does not create sample users, other user types, OpenChoreo clients, branding, or shared server configuration. It uses a separate role granting the existing System resource server's `system` permission to `amp-system-client`, rather than rewriting Thunder's built-in Administrator role.

## 4. Validate and import

### Trust OpenChoreo's CA

The tool verifies Thunder's HTTPS certificate. OpenChoreo's reference installation issues it from a self-signed CA (`openchoreo-ca`), which your machine does not trust. Export that CA before the first command when it exists; otherwise the variable stays empty and the system trust store is used:

```bash
export OPENCHOREO_CA_FILE=""
if kubectl get secret openchoreo-ca-secret -n cert-manager >/dev/null 2>&1; then
  export OPENCHOREO_CA_FILE="${AMP_IDENTITY_WORK}/openchoreo-ca.crt"
  kubectl get secret openchoreo-ca-secret -n cert-manager \
    -o jsonpath='{.data.ca\.crt}' | base64 -d > "${OPENCHOREO_CA_FILE}"
fi
echo "OPENCHOREO_CA_FILE=${OPENCHOREO_CA_FILE:-<system trust store>}"
```

Skip this block if you already set `OPENCHOREO_CA_FILE` while following the README. Every command below passes it with `--ca-file`, in addition to the system trust store. If your certificates come from another private CA, set `OPENCHOREO_CA_FILE` to that CA's PEM file. The tool is Python, so `CURL_CA_BUNDLE` has no effect on it.

If Thunder is served over plain HTTP, as in OpenChoreo's local k3d setup, no CA is involved: leave `OPENCHOREO_CA_FILE` empty and add `--allow-http` to every command below.

### Run the import

`THUNDER_ADMIN_URL` is the base URL the tool calls Thunder at. The block uses the public issuer URL; set `THUNDER_ADMIN_URL` to another reachable address for the same Thunder first if your machine cannot reach the public one. The tool checks that the Thunder it reaches advertises the issuer in `thunderPublicUrl`. `THUNDER_ADMIN_TOKEN` must already contain your system-scoped administrative token.

```bash
export THUNDER_ADMIN_URL="${THUNDER_ADMIN_URL:-${THUNDER_PUBLIC_URL}}"
: "${THUNDER_ADMIN_TOKEN:?Set a system-scoped Thunder administrative token}"

.venv/bin/python scripts/provision-thunder-identities.py dry-run \
  --bundle-dir "${AMP_IDENTITY_WORK}/rendered" \
  --thunder-url "${THUNDER_ADMIN_URL}" \
  --ca-file "${OPENCHOREO_CA_FILE}"
```

Review the rendered definitions privately, then apply:

```bash
.venv/bin/python scripts/provision-thunder-identities.py apply \
  --bundle-dir "${AMP_IDENTITY_WORK}/rendered" \
  --thunder-url "${THUNDER_ADMIN_URL}" \
  --ca-file "${OPENCHOREO_CA_FILE}"
```

For an explicitly trusted local HTTP connection, add `--allow-http`. HTTPS certificates are verified, and redirects are refused so credentials are not forwarded to another endpoint.

Both commands verify the organization and System resource identifier and reject existing resource IDs. `apply` runs a dry-run before a create-only import, with `upsert=false` and `continueOnError=false`. The tool checks every returned result, including responses with HTTP 200 that contain failed items.

Thunder's dry-run does not validate every create-time constraint, such as client-ID or role-name collisions. Import is not a transaction: if a later document fails, earlier resources may already exist. Inspect the results in Thunder before recovering; do not switch to an unrestricted upsert or delete shared resources to retry.

## 5. Configure shared settings

AMP needs two shared Thunder settings that the identity bundle does not create:

| Setting | Why AMP needs it |
|---|---|
| Default resource server set to `urn:wso2:amp` | AMP v1.0.0 makes scoped client-credentials requests without an OAuth `resource` parameter |
| AMP console origin in the CORS allowed origins | The console exchanges its login code for a token from the browser |

A stock OpenChoreo v1.3.x installation sets neither. Run this after `apply`, because the default must point at the imported `amp-resource-server`. Without `--apply` it only reports what it would change:

```bash
.venv/bin/python scripts/provision-thunder-identities.py configure-shared-settings \
  --bundle-dir "${AMP_IDENTITY_WORK}/rendered" \
  --thunder-url "${THUNDER_ADMIN_URL}" \
  --ca-file "${OPENCHOREO_CA_FILE}"
```

Review the output, then make the changes:

```bash
.venv/bin/python scripts/provision-thunder-identities.py configure-shared-settings \
  --bundle-dir "${AMP_IDENTITY_WORK}/rendered" \
  --thunder-url "${THUNDER_ADMIN_URL}" \
  --ca-file "${OPENCHOREO_CA_FILE}" \
  --apply
```

The command writes each setting through Thunder's server-configuration API, which validates a value before storing it. It changes only what is missing:

- **Default resource server:** set only when none is configured. ThunderID consults the default only for a request that carries non-OIDC scopes and no `resource` parameter, and rejects that request with `invalid_target` while no default exists. Adding a default therefore affects only requests that fail today. If a *different* default is already configured, the command stops without changing it, because replacing it would rebind requests that currently succeed. Resolve that case with the owners of the existing consumers.
- **CORS:** appends the console origin to the writable layer and keeps every existing entry, including regex entries. Thunder combines this layer with the read-only origins from its Helm values.

Do not proceed with AMP installation while the following check fails:

```bash
.venv/bin/python scripts/provision-thunder-identities.py check-readiness \
  --bundle-dir "${AMP_IDENTITY_WORK}/rendered" \
  --thunder-url "${THUNDER_ADMIN_URL}" \
  --ca-file "${OPENCHOREO_CA_FILE}"
```

This checks the effective `merged` server configuration, rather than assuming a Helm value took effect. It verifies the organization, System identifier, AMP default resource server, and console CORS origin. It does not validate login flows or prove that all existing OpenChoreo consumers remain compatible, so test existing OpenChoreo login, builds, and observer requests after this step.

## 6. Connect the AMP services

There is nothing to run in this step. When you follow the README, its installation steps connect each service to the identities you created:

| README step | Connects |
|---|---|
| [Step 2](README.md#store-agent-manager-secrets-in-openbao) | Stores the `amp-publisher-client` and `amp-system-client` secrets in OpenBao |
| [Step 4](README.md#step-4-install-agent-manager-core) | Installs the Agent Manager API and console with the rendered `agent-manager.yaml` |
| [Step 6](README.md#step-6-install-platform-resources) | Adds the OpenChoreo authorization bindings for `amp-api-client`, `am-observer-client`, and the workload publisher |
| [Step 7](README.md#step-7-install-observability-extension) | Configures the observer with `am-observer-client` |
| [Step 9](README.md#step-9-install-api-platform-gateway-extension) | Gives the gateway bootstrap the `amp-api-client` credentials |

Keep the shell from step 2 open: the README steps use the client secrets exported there. The rest of this section explains what those steps configure, for installations that do not follow the README.

Use the rendered `agent-manager.yaml` with `oci://ghcr.io/wso2/wso2-agent-manager --version 1.0.0`. It configures the public issuer, browser redirects, internal token/JWKS endpoints, system client, public API/observer URLs, and `tlsEnabled`, which the renderer sets from the scheme of `apiPublicUrl`. It leaves `keyManager.audience` at the chart default, which accepts the console, CLI, publisher, and MCP clients; the chart adds the API MCP audience from `serverPublicURL`.

The rendered file covers identity and routing only. It is not enough to install the chart on its own: install it together with the module's deployment values, [`values/agent-manager.yaml`](values/agent-manager.yaml), as shown in [Step 4 of the README](README.md#step-4-install-agent-manager-core). That file supplies the settings that otherwise fall back to development defaults:

| Value | Chart default and its effect |
|---|---|
| `agentManagerService.config.openbao.existingSecret` | Dev-mode token `root`, which a sealed OpenBao rejects |
| `agentManagerService.config.workflowPlaneOpenbao.existingSecret` and `.existingSecretKey` | Dev-mode token `root`; Git-credential reads fail, so the repository and branch pickers return errors |
| `agentManagerService.replicaCount`, `agentManagerService.autoscaling.enabled`, `console.replicaCount` | Autoscaling up to 10 API replicas, which needs the Redis gateway manifest cache; the module runs one API replica instead |
| `postgresql.enabled=false` and `postgresql.external.*` (commented out) | In-cluster PostgreSQL with a default password |

Pass the deployment values first and the rendered file last, so the identity settings take precedence.

Wire the other clients using `extension-credentials.json`:

| Consumer | Configuration |
|---|---|
| Evaluation workflow | `amp-publisher-client`; store its secret at the release's expected OpenBao key `secret/amp-publisher-client-secret` |
| AMP observer | `amObserver.observer.idpClientId`, token endpoint, and matching secret or `existingSecret` |
| Gateway bootstrap | The same `amp-api-client` credentials used by the AMP API; configure its existing-secret reference |

Reuse `openchoreo-workload-publisher-client` for builds and preserve `openchoreo-observer-resource-reader-client` for OpenChoreo's observer. Add the v1.0.0 OpenChoreo authorization bindings for AMP API access, AMP observer reads, and the workload publisher's additional deployment actions. Identity registration alone does not grant OpenChoreo API access.

When OpenChoreo extracts service-account identities from token claims, its configuration and bindings must agree with the actual `client_id` claim. Do not rewrite every existing binding without checking the other clients.

Verify console login, issued `iss`/`aud`/`scope` and organization claims, a system-client administration request, observer reads, and evaluation score publishing. Check that existing OpenChoreo login, builds, and observer requests still work. Do not share tokens or secrets in test reports.

## Remove the identities

Run this after Agent Manager is uninstalled (see the README's Uninstallation section), so it cannot recreate anything meanwhile. It is also required before installing again: the import refuses to overwrite existing AMP resources.

`remove` needs only the non-secret `configuration.yaml` from [step 2](#2-prepare-the-configuration-and-client-secrets), not the rendered bundle. If you no longer have it, recreate it with the organization unit ID, the System resource server ID, and the Thunder and console public URLs. Request a fresh `THUNDER_ADMIN_TOKEN` as in [step 1](#1-collect-the-existing-identity-configuration), then review what would change:

```bash
.venv/bin/python scripts/provision-thunder-identities.py remove \
  --config "${AMP_IDENTITY_WORK}/configuration.yaml" \
  --thunder-url "${THUNDER_ADMIN_URL}" \
  --ca-file "${OPENCHOREO_CA_FILE}"
```

It reports only what exists, in this order:

1. Clears the default resource server, but only if it is still the AMP resource server, and removes the AMP console origin from the CORS writable layer. Other origins and a default set by someone else are kept.
2. Deletes users of the `engineer` type, which were created from the AMP console, and the `amp-publisher-<organization-id>` and `amp-scheduler-<organization-id>` clients Agent Manager created at runtime.
3. Deletes the AMP roles, the administrators group, the AMP clients, the AMP resource servers, and the `engineer` user type.

OpenChoreo's clients, users of other types, signing keys, and built-in roles are not touched. Review the list, then remove:

```bash
.venv/bin/python scripts/provision-thunder-identities.py remove \
  --config "${AMP_IDENTITY_WORK}/configuration.yaml" \
  --thunder-url "${THUNDER_ADMIN_URL}" \
  --ca-file "${OPENCHOREO_CA_FILE}" \
  --apply
```

Shared settings are reverted before anything is deleted, and the command stops without deleting if Thunder still uses the AMP default. Run `remove` again without `--apply`; it reports `Nothing to remove` once Thunder is clean.

## Reference and maintenance

- [AMP v1.0.0 bootstrap definitions](https://github.com/wso2/agent-manager/blob/amp/v1.0.0/deployments/helm-charts/wso2-amp-thunder-extension/templates/amp-thunder-bootstrap.yaml)
- [AMP v1.0.0 client configuration](https://github.com/wso2/agent-manager/blob/amp/v1.0.0/deployments/helm-charts/wso2-amp-thunder-extension/values.yaml)
- [ThunderID v1.0.0 import API](https://github.com/thunder-id/thunderid/blob/v1.0.0/api/import.yaml)
- [ThunderID server configuration API](https://github.com/thunder-id/thunderid/blob/v1.0.0/api/server-config.yaml)

To regenerate the template, use a local Agent Manager checkout containing the `amp/v1.0.0` tag and Helm 3:

```bash
.venv/bin/python scripts/generate-thunder-identities.py /path/to/agent-manager
make unit-test
```

`make unit-test` is the same target PR CI runs for this module.

The generator selects AMP-owned definitions from the pinned release and adapts their references. Do not edit the generated identity template manually.
