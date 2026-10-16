#!/usr/bin/env python3
# Copyright 2026 The OpenChoreo Authors
# SPDX-License-Identifier: Apache-2.0
"""Render, validate, and import AMP 1.0.0 identities into ThunderID 1.0.0."""
import argparse
import json
import os
from pathlib import Path
import re
import ssl
import sys
from urllib.error import HTTPError, URLError
from urllib.parse import quote, urlsplit
from urllib.request import HTTPRedirectHandler, HTTPSHandler, Request, build_opener

import yaml

MODULE = Path(__file__).resolve().parents[1]
SECRET_NAMES = (
    "AMP_API_CLIENT_SECRET", "AMP_SYSTEM_CLIENT_SECRET",
    "AMP_PUBLISHER_CLIENT_SECRET", "AM_OBSERVER_CLIENT_SECRET",
)
PLACEHOLDER = re.compile(r"\$\{([A-Z_]+)\}")
PATHS = {"application": "applications", "role": "roles", "group": "groups",
         "resource_server": "resource-servers", "user_type": "user-types"}
AMP_RESOURCE_SERVER_ID = "amp-resource-server"
AMP_RESOURCE_IDENTIFIER = "urn:wso2:amp"
# The AMP console creates users with this user type, by name.
AMP_USER_TYPE = "engineer"


class ProvisionError(Exception):
    pass


def require(condition, message):
    if not condition:
        raise ProvisionError(message)


def required_text(config, key):
    value = config.get(key)
    require(isinstance(value, str) and value.strip() and not any(c in value for c in "<>\r\n"),
            f"Set {key} in the configuration.")
    return value


def url(value):
    parsed = urlsplit(value)
    require(parsed.scheme in ("http", "https") and parsed.hostname
            and not parsed.username and not parsed.password and not parsed.query and not parsed.fragment,
            "URLs must be absolute HTTP(S) URLs without credentials, query, or fragment.")
    require(not parsed.hostname.endswith(".example.com"), "Replace the example.com URLs with your deployment addresses.")
    return value.rstrip("/")


def expand(value, variables):
    if isinstance(value, dict):
        return {key: expand(item, variables) for key, item in value.items()}
    if isinstance(value, list):
        return [expand(item, variables) for item in value]
    if not isinstance(value, str):
        return value
    match = PLACEHOLDER.fullmatch(value)
    if match:
        require(match[1] in variables, f"Unknown template variable {match[1]}.")
        return variables[match[1]]
    def substitute(match):
        require(match[1] in variables and isinstance(variables[match[1]], str),
                f"Invalid string template variable {match[1]}.")
        return variables[match[1]]
    return PLACEHOLDER.sub(substitute, value)


def variables_for(config):
    variables = {}
    for key, variable in {
        "organizationUnitId": "OU_ID", "organizationUnitHandle": "OU_HANDLE",
        "authFlowId": "AUTH_FLOW_ID", "systemResourceServerId": "THUNDER_SYSTEM_RESOURCE_ID",
    }.items():
        variables[variable] = required_text(config, key)
    # The released backend resolves the default OU; other handles need a
    # separately validated multi-organization integration.
    require(variables["OU_HANDLE"] == "default", "AMP 1.0.0's documented module setup requires the default OU.")
    for key in ("allowedUserTypes", "adminUserIds"):
        values = config.get(key)
        require(isinstance(values, list) and values, f"Set at least one entry in {key}.")
        for value in values:
            required_text({key: value}, key)
    require(AMP_USER_TYPE in config["allowedUserTypes"],
            f"Add {AMP_USER_TYPE} to allowedUserTypes. The AMP console creates users with that type, and they cannot sign in without it.")
    variables["USER_TYPES"] = config["allowedUserTypes"]
    variables["ADMIN_MEMBERS"] = [{"id": value, "type": "user"} for value in config["adminUserIds"]]
    for key, variable in {
        "thunderPublicUrl": "THUNDER_URL", "consolePublicUrl": "CONSOLE_URL",
        "apiPublicUrl": "API_URL", "observerPublicUrl": "OBSERVER_URL",
        "instrumentationUrl": "INSTRUMENTATION_URL", "thunderTokenUrl": "TOKEN_URL",
        "thunderJwksUrl": "JWKS_URL", "openChoreoApiUrl": "OPENCHOREO_URL",
    }.items():
        variables[variable] = url(required_text(config, key))
    for variable in ("THUNDER_URL", "CONSOLE_URL", "API_URL", "OBSERVER_URL"):
        require(not urlsplit(variables[variable]).path, f"{variable} must be an origin without a path.")
    dial_host = config.get("thunderResolveToHost", "")
    require(isinstance(dial_host, str) and not any(c in dial_host for c in "/@<>\r\n"),
            "thunderResolveToHost must be empty or host:port.")
    variables["THUNDER_DIAL_HOST"] = dial_host
    variables["CONSOLE_HOST"] = urlsplit(variables["CONSOLE_URL"]).hostname
    variables["API_HOST"] = urlsplit(variables["API_URL"]).hostname
    # Selects the https variant of agent invoke URLs. The chart types the core
    # value as a boolean and the console value as a string.
    tls = urlsplit(variables["API_URL"]).scheme == "https"
    variables["TLS_ENABLED"] = tls
    variables["TLS_ENABLED_TEXT"] = "true" if tls else "false"
    for name in SECRET_NAMES:
        secret = os.environ.get(name, "")
        require(len(secret) >= 32, f"Set {name} to a stable secret of at least 32 characters.")
        variables[name] = secret
    return variables


def private_write(path, content):
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "w") as stream:
        stream.write(content)


def render(config_path, output):
    config = yaml.safe_load(config_path.read_text())
    require(isinstance(config, dict), "Configuration must be a YAML mapping.")
    variables = variables_for(config)
    documents = [expand(d, variables) for d in yaml.safe_load_all(
        (MODULE / "resources/amp-thunder-identities.yaml").read_text()) if d]
    core = expand(yaml.safe_load((MODULE / "values/agent-manager-v1.yaml").read_text()), variables)
    # Validate before creating any output. Use a new private directory, never
    # overwrite previously rendered credentials or follow an existing symlink.
    output.mkdir(mode=0o700, parents=False, exist_ok=False)
    private_write(output / "identities.yaml", yaml.safe_dump_all(documents, sort_keys=False))
    private_write(output / "agent-manager.yaml", yaml.safe_dump(core, sort_keys=False))
    private_write(output / "deployment.json", json.dumps(config, indent=2) + "\n")
    # Consumers of these credentials live in different namespaces. Keep the
    # exact values alongside the bundle so the same secrets can be provisioned.
    private_write(output / "extension-credentials.json", json.dumps({
        "evaluationPublisher": {"clientId": "amp-publisher-client", "clientSecret": variables["AMP_PUBLISHER_CLIENT_SECRET"]},
        "observer": {"clientId": "am-observer-client", "clientSecret": variables["AM_OBSERVER_CLIENT_SECRET"]},
    }, indent=2) + "\n")
    print(f"Rendered {len(documents)} identities and matching core values. Output contains secrets; keep it outside Git.")


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class Thunder:
    def __init__(self, base_url, token, allow_http=False, ca_file=None):
        self.base_url = url(base_url)
        require(urlsplit(self.base_url).scheme == "https" or allow_http,
                "Use HTTPS, or --allow-http for an explicitly trusted local connection.")
        require(bool(token) and "\n" not in token and "\r" not in token, "Set THUNDER_ADMIN_TOKEN to a system-scoped token.")
        self.token = token
        handlers = [NoRedirect()]
        if ca_file:
            # Trust a private CA, such as the self-signed CA of OpenChoreo's reference
            # installation, in addition to the system trust store.
            require(Path(ca_file).is_file(), f"CA file {ca_file} does not exist.")
            context = ssl.create_default_context()
            context.load_verify_locations(cafile=ca_file)
            handlers.append(HTTPSHandler(context=context))
        self.opener = build_opener(*handlers)

    def request(self, path, payload=None, missing_ok=False, method=None):
        request = Request(self.base_url + path,
                          data=None if payload is None else json.dumps(payload).encode(),
                          headers={"Authorization": "Bearer " + self.token, "Content-Type": "application/json"},
                          method=method)
        try:
            with self.opener.open(request, timeout=30) as response:
                body = response.read()
                return json.loads(body) if body.strip() else {}
        except HTTPError as error:
            error.close()
            if error.code == 404 and missing_ok:
                return None
            # Never echo responses: identity APIs can return credentials.
            raise ProvisionError(f"Thunder returned HTTP {error.code}; response omitted to protect credentials.") from None
        except (URLError, TimeoutError, ValueError):
            raise ProvisionError("Thunder request failed or returned invalid JSON; details omitted to protect credentials.") from None


def load_bundle(directory):
    config = json.loads((directory / "deployment.json").read_text())
    documents = list(yaml.safe_load_all((directory / "identities.yaml").read_text()))
    require(documents and all(isinstance(d, dict) and d.get("resource_type") in PATHS
                             and (str(d.get("id", "")).startswith(("amp-", "am-", "amctl-"))
                                  or (d.get("resource_type") == "user_type" and d.get("id") == AMP_USER_TYPE))
                             for d in documents),
            "Bundle must contain only AMP applications, groups, roles, resource servers, and the engineer user type.")
    require(all(d.get("ouId") == config["organizationUnitId"] for d in documents), "Bundle OU does not match deployment.json.")
    return config, documents


def preflight(client, config):
    discovery = client.request("/.well-known/openid-configuration")
    require(discovery.get("issuer") == config["thunderPublicUrl"].rstrip("/"),
            "Thunder issuer does not match the configured public URL.")
    unit = client.request("/organization-units/tree/" + quote(config["organizationUnitHandle"], safe=""))
    require(unit.get("id") == config["organizationUnitId"], "Existing organization ID does not match configuration.")
    server = client.request("/resource-servers/" + quote(config["systemResourceServerId"], safe=""))
    require(server.get("identifier") == config["thunderPublicUrl"].rstrip("/") + "/mcp",
            "Thunder's System resource identifier must match thunderPublicUrl + /mcp. No shared configuration was changed.")


def validate_result(result, count):
    summary = result.get("summary", {})
    items = result.get("results", [])
    require(summary.get("totalDocuments") == count and summary.get("imported") == count
            and summary.get("failed") == 0 and len(items) == count
            and all(item.get("status") == "success" and item.get("operation") == "create" for item in items),
            "Import did not report a successful creation for every resource. Inspect Thunder privately; earlier writes may remain.")


def import_bundle(client, config, documents, apply=False):
    preflight(client, config)
    for document in documents:
        path = "/" + PATHS[document["resource_type"]] + "/" + quote(document["id"], safe="")
        require(client.request(path, missing_ok=True) is None,
                f"Resource {document['id']} already exists. Reconcile it separately; this tool never overwrites identities.")
    payload = {"content": yaml.safe_dump_all(documents, sort_keys=False), "dryRun": True,
               "options": {"upsert": False, "continueOnError": False, "target": "runtime"}}
    validate_result(client.request("/import", payload), len(documents))
    if apply:
        validate_result(client.request("/import", {**payload, "dryRun": False}), len(documents))
        print("AMP identities imported. Run check-readiness and configure the matching service credentials next.")
    else:
        print("Thunder dry-run passed. It does not validate every dependency or guarantee a successful live import.")


def check_readiness(client, config):
    preflight(client, config)
    setting = client.request("/server-config/defaultResourceServer")
    resource_id = setting.get("merged", {}).get("resourceServerId")
    require(isinstance(resource_id, str) and resource_id, "Thunder has no default resource server. AMP's scoped token requests require an AMP default.")
    server = client.request("/resource-servers/" + quote(resource_id, safe=""))
    require(server.get("identifier") == AMP_RESOURCE_IDENTIFIER,
            "Shared default resource server is not urn:wso2:amp. AMP 1.0.0 sends scoped requests without resource; resolve this compatibility requirement before installation. No setting was changed.")
    cors = client.request("/server-config/cors")
    require(config["consolePublicUrl"].rstrip("/") in cors.get("merged", {}).get("allowedOrigins", []),
            "Add the AMP console origin to Thunder's existing CORS origins without removing existing entries.")
    user_types = client.request("/user-types?limit=100").get("types", [])
    require(any(t.get("name") == AMP_USER_TYPE and t.get("ouId") == config["organizationUnitId"] for t in user_types),
            f"Thunder has no {AMP_USER_TYPE} user type in the AMP organization. The AMP console creates users with it; import the AMP identities.")
    print("Organization, System resource identifier, default resource server, console CORS, and user type checks passed. Test login, token claims, and OpenChoreo authorization separately.")


def configure_shared_settings(client, config, apply=False):
    # ThunderID consults the default resource server only for a token or
    # authorization request that carries non-OIDC scopes and no resource
    # parameter, and rejects that request with invalid_target while no default
    # is set. Setting a default where none exists therefore changes only
    # requests that fail today. Replacing an existing default would rebind
    # working requests, so that is refused.
    preflight(client, config)
    changes = []
    setting = client.request("/server-config/defaultResourceServer")
    current = setting.get("merged", {}).get("resourceServerId")
    if current:
        server = client.request("/resource-servers/" + quote(current, safe=""))
        require(server.get("identifier") == AMP_RESOURCE_IDENTIFIER,
                "Thunder already has a different default resource server. Changing it would rebind existing token requests; this tool never replaces it. No setting was changed.")
    else:
        server = client.request("/resource-servers/" + AMP_RESOURCE_SERVER_ID, missing_ok=True)
        require(server is not None and server.get("identifier") == AMP_RESOURCE_IDENTIFIER,
                "The AMP resource server is missing. Import the identity bundle with apply first.")
        changes.append(("/server-config/defaultResourceServer", {"resourceServerId": AMP_RESOURCE_SERVER_ID},
                        "set the default resource server to urn:wso2:amp"))
    # CORS unions its read-only and writable layers, so appending to the
    # writable layer keeps every existing origin, including regex entries.
    origin = config["consolePublicUrl"].rstrip("/")
    cors = client.request("/server-config/cors")
    if origin not in cors.get("merged", {}).get("allowedOrigins", []):
        writable = (cors.get("writable") or {}).get("allowedOrigins") or []
        require(isinstance(writable, list), "Thunder's writable CORS layer has an unexpected shape. No setting was changed.")
        changes.append(("/server-config/cors", {**(cors.get("writable") or {}), "allowedOrigins": writable + [origin]},
                        "add the AMP console origin to the CORS allowed origins"))
    if not changes:
        print("Shared settings already meet AMP's requirements. Nothing to change.")
        return
    if not apply:
        for _, _, description in changes:
            print("Would " + description + ".")
        print("Re-run with --apply to make these changes.")
        return
    for path, payload, description in changes:
        client.request(path, payload, method="PUT")
        print("Changed: " + description + ".")
    print("Run check-readiness to confirm the effective configuration.")


# Agent Manager creates these clients itself at runtime, named after the OU ID.
RUNTIME_APP_PREFIXES = ("amp-publisher-", "amp-scheduler-")
# Delete dependents before what they reference.
REMOVAL_ORDER = ("role", "group", "application", "resource_server", "user_type")


def list_all(client, path, key):
    """Read every page of a Thunder list endpoint."""
    items, offset = [], 0
    separator = "&" if "?" in path else "?"
    while True:
        page = client.request(f"{path}{separator}limit=100&offset={offset}").get(key) or []
        items.extend(page)
        if len(page) < 100:
            return items
        offset += len(page)


def resource_tree_paths(client, server_id):
    """Paths under a resource server, ordered so each can be deleted after its dependents.

    Thunder refuses to delete a resource server that has resources or actions,
    and a resource that has sub-resources or actions.
    """
    base = "/resource-servers/" + quote(server_id, safe="")
    paths = []

    def walk(parent_id):
        query = base + "/resources" + ("" if parent_id is None else "?parentId=" + quote(parent_id, safe=""))
        for resource in list_all(client, query, "resources"):
            resource_path = base + "/resources/" + quote(resource["id"], safe="")
            walk(resource["id"])
            for action in list_all(client, resource_path + "/actions", "actions"):
                paths.append(resource_path + "/actions/" + quote(action["id"], safe=""))
            paths.append(resource_path)

    walk(None)
    for action in list_all(client, base + "/actions", "actions"):
        paths.append(base + "/actions/" + quote(action["id"], safe=""))
    return paths


def load_removal_inputs(config_path):
    """Read the non-secret configuration and the template's fixed resource IDs.

    Removal never needs the rendered bundle, which holds credentials and is
    not kept after installation.
    """
    config = yaml.safe_load(config_path.read_text())
    require(isinstance(config, dict), "Configuration must be a YAML mapping.")
    require(not str(config.get("organizationUnitId", "")).startswith("<"),
            "The configuration is the unfilled template, so it cannot identify what to remove. Use the "
            "configuration the identities were imported with; if they were never imported, nothing needs removing.")
    for key in ("organizationUnitId", "organizationUnitHandle", "systemResourceServerId"):
        required_text(config, key)
    for key in ("thunderPublicUrl", "consolePublicUrl"):
        config[key] = url(required_text(config, key))
    documents = [d for d in yaml.safe_load_all((MODULE / "resources/amp-thunder-identities.yaml").read_text()) if d]
    return config, documents


def in_organization_unit(client, collection, item, ou):
    """Whether a listed user or application belongs to organization unit ou.
    ThunderID 1.0 lists applications without their ouId, so read the item
    itself when the list omits it. An item whose unit stays unknown is kept."""
    if item.get("ouId"):
        return item["ouId"] == ou
    detail = client.request("/" + collection + "/" + quote(item["id"], safe=""), missing_ok=True) or {}
    return detail.get("ouId") == ou


def removal_plan(client, config, documents):
    """Return the shared-setting reversals and deletions that remove AMP from Thunder."""
    ou = config["organizationUnitId"]
    changes = []
    setting = client.request("/server-config/defaultResourceServer")
    if (setting.get("writable") or {}).get("resourceServerId") == AMP_RESOURCE_SERVER_ID:
        changes.append(("/server-config/defaultResourceServer", {"resourceServerId": ""},
                        "clear the default resource server (currently the AMP resource server)"))
    origin = config["consolePublicUrl"].rstrip("/")
    cors = client.request("/server-config/cors")
    writable = (cors.get("writable") or {}).get("allowedOrigins") or []
    if origin in writable:
        changes.append(("/server-config/cors", {**(cors.get("writable") or {}),
                                                "allowedOrigins": [o for o in writable if o != origin]},
                        "remove the AMP console origin from the CORS allowed origins"))
    deletions = []
    # Users of the AMP user type were created from the AMP console; the type
    # cannot be removed while they exist.
    for user in list_all(client, "/users", "users"):
        if user.get("type") == AMP_USER_TYPE and in_organization_unit(client, "users", user, ou):
            name = (user.get("attributes") or {}).get("username") or user["id"]
            deletions.append(("/users/" + quote(user["id"], safe=""), f"user {name} (type {AMP_USER_TYPE})"))
    for app in list_all(client, "/applications", "applications"):
        if str(app.get("name", "")).startswith(RUNTIME_APP_PREFIXES) and in_organization_unit(client, "applications", app, ou):
            deletions.append(("/applications/" + quote(app["id"], safe=""), f"application {app['name']} (created by Agent Manager)"))
    wanted = {(d["resource_type"], d["id"]) for d in documents} | {("user_type", AMP_USER_TYPE)}
    for kind in REMOVAL_ORDER:
        for _, identifier in sorted(w for w in wanted if w[0] == kind):
            path = "/" + PATHS[kind] + "/" + quote(identifier, safe="")
            if client.request(path, missing_ok=True) is None:
                continue
            description = f"{kind.replace('_', ' ')} {identifier}"
            if kind == "resource_server":
                children = resource_tree_paths(client, identifier)
                deletions.extend((child, None) for child in children)
                if children:
                    description += f" (with its {len(children)} resources and actions)"
            deletions.append((path, description))
    return changes, deletions


def remove_identities(client, config, documents, apply=False):
    preflight(client, config)
    changes, deletions = removal_plan(client, config, documents)
    if not changes and not deletions:
        print("No AMP identities or shared-setting changes remain in Thunder. Nothing to remove.")
        return
    if not apply:
        for _, _, description in changes:
            print("Would " + description + ".")
        for _, description in deletions:
            if description:
                print("Would delete " + description + ".")
        print("Re-run with --apply to make these changes.")
        return
    # Revert shared settings first: Thunder must not keep a default that points
    # at a resource server about to be deleted.
    for path, payload, description in changes:
        client.request(path, payload, method="PUT")
        print("Changed: " + description + ".")
    setting = client.request("/server-config/defaultResourceServer")
    require(setting.get("merged", {}).get("resourceServerId") != AMP_RESOURCE_SERVER_ID,
            "Thunder still uses the AMP resource server as its default. No identity was deleted.")
    for path, description in deletions:
        client.request(path, method="DELETE")
        if description:
            print("Deleted: " + description + ".")
    print("AMP identities removed from Thunder. Run remove again to confirm nothing remains.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)
    renderer = subparsers.add_parser("render")
    renderer.add_argument("--config", type=Path, required=True)
    renderer.add_argument("--output-dir", type=Path, required=True)
    for command in ("dry-run", "apply", "configure-shared-settings", "check-readiness", "remove"):
        child = subparsers.add_parser(command)
        if command == "remove":
            child.add_argument("--config", type=Path, required=True, help="The non-secret configuration used to render the bundle.")
        else:
            child.add_argument("--bundle-dir", type=Path, required=True)
        child.add_argument("--thunder-url", required=True, help="Reachable Thunder base URL; must issue the configured public issuer.")
        child.add_argument("--allow-http", action="store_true")
        child.add_argument("--ca-file", help="PEM file of an additional CA to trust for Thunder's HTTPS certificate.")
        if command in ("configure-shared-settings", "remove"):
            child.add_argument("--apply", action="store_true", help="Make the reported changes. Without it, only report them.")
    args = parser.parse_args()
    try:
        if args.command == "render":
            render(args.config, args.output_dir)
        else:
            if args.command == "remove":
                config, documents = load_removal_inputs(args.config)
            else:
                config, documents = load_bundle(args.bundle_dir)
            client = Thunder(args.thunder_url, os.environ.get("THUNDER_ADMIN_TOKEN", ""), args.allow_http, args.ca_file)
            if args.command == "check-readiness":
                check_readiness(client, config)
            elif args.command == "configure-shared-settings":
                configure_shared_settings(client, config, apply=args.apply)
            elif args.command == "remove":
                remove_identities(client, config, documents, apply=args.apply)
            else:
                import_bundle(client, config, documents, apply=args.command == "apply")
    except (ProvisionError, OSError, ValueError, KeyError, TypeError, yaml.YAMLError) as error:
        # Only controlled errors are safe to print; parser exceptions can
        # include source lines containing credentials. A missing file's path
        # is safe and tells the operator which step to run.
        if isinstance(error, ProvisionError):
            message = str(error)
        elif isinstance(error, FileNotFoundError) and error.filename:
            message = f"File not found: {error.filename}."
            if Path(error.filename).name in ("deployment.json", "identities.yaml"):
                message += " Render the bundle into that directory first, and check AMP_IDENTITY_WORK."
        else:
            message = "Invalid input or filesystem error; details omitted to protect credentials."
        print(message, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
