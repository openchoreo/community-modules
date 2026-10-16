# Copyright 2026 The OpenChoreo Authors
# SPDX-License-Identifier: Apache-2.0
import importlib.util
import io
import json
import os
from pathlib import Path
import tempfile
import unittest
from contextlib import redirect_stdout
from unittest.mock import patch
from urllib.error import HTTPError

import yaml

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("provision", ROOT / "scripts/provision-thunder-identities.py")
provision = importlib.util.module_from_spec(spec)
spec.loader.exec_module(provision)


def config():
    return {
        "organizationUnitId": "real-existing-ou", "organizationUnitHandle": "default",
        "authFlowId": "existing-flow", "allowedUserTypes": ["openchoreo-user", "engineer"],
        "adminUserIds": ["existing-admin"], "systemResourceServerId": "existing-system-rs",
        "thunderPublicUrl": "https://id.company.test", "consolePublicUrl": "https://amp.company.test",
        "apiPublicUrl": "https://api.company.test", "observerPublicUrl": "https://obs.company.test",
        "instrumentationUrl": "https://gateway.company.test/otel",
        "thunderTokenUrl": "https://id.company.test/oauth2/token",
        "thunderJwksUrl": "https://id.company.test/oauth2/jwks",
        "thunderResolveToHost": "", "openChoreoApiUrl": "http://openchoreo-api.namespace.svc:8080",
    }


def app_name(app):
    return app[0] if isinstance(app, tuple) else app


class FakeThunder:
    def __init__(self, failed=False, existing=False, default="urn:wso2:amp", issuer="https://id.company.test",
                 readonly_origins=("https://openchoreo.company.test",),
                 writable_origins=("https://amp.company.test",), amp_resource_server=False,
                 user_types=("openchoreo-user", "engineer"), writable_default=None, users=(), runtime_apps=()):
        self.calls = []
        self.failed, self.existing, self.default, self.issuer = failed, existing, default, issuer
        self.readonly_origins, self.writable_origins = list(readonly_origins), list(writable_origins)
        self.amp_resource_server = amp_resource_server
        self.user_types = list(user_types)
        self.writable_default, self.users, self.runtime_apps = writable_default, list(users), list(runtime_apps)

    # amp-resource-server holds: resource r1 -> child r2 (action a2), r1 action a1,
    # and a server-level action s1. Other servers are empty.
    def resource_tree(self, path):
        if not path.startswith("/resource-servers/amp-resource-server/"):
            return {"resources": [], "actions": []}
        tail = path[len("/resource-servers/amp-resource-server"):]
        return {
            "/resources?limit=100&offset=0": {"resources": [{"id": "r1"}]},
            "/resources?parentId=r1&limit=100&offset=0": {"resources": [{"id": "r2"}]},
            "/resources?parentId=r2&limit=100&offset=0": {"resources": []},
            "/resources/r2/actions?limit=100&offset=0": {"actions": [{"id": "a2"}]},
            "/resources/r1/actions?limit=100&offset=0": {"actions": [{"id": "a1"}]},
            "/actions?limit=100&offset=0": {"actions": [{"id": "s1"}]},
        }[tail]

    def request(self, path, payload=None, missing_ok=False, method=None):
        self.calls.append((path, payload))
        self.methods = getattr(self, "methods", []) + [(method, path)]
        if method in ("PUT", "DELETE"):
            return {}
        if path == "/.well-known/openid-configuration":
            return {"issuer": self.issuer}
        if path == "/organization-units/tree/default":
            return {"id": "real-existing-ou"}
        if path == "/resource-servers/existing-system-rs":
            return {"identifier": "https://id.company.test/mcp"}
        if path == "/server-config/defaultResourceServer":
            if self.writable_default:
                cleared = any(p == path and payload == {"resourceServerId": ""} for p, payload in self.calls[:-1])
                current = "" if cleared else self.writable_default
                return {"writable": {"resourceServerId": current}, "merged": {"resourceServerId": current}}
            return {"merged": {"resourceServerId": "current-default"} if self.default else {}}
        if path == "/resource-servers/current-default":
            return {"identifier": self.default}
        if path == "/resource-servers/amp-resource-server":
            return {"identifier": "urn:wso2:amp"} if self.amp_resource_server else None
        if path == "/server-config/cors":
            return {"readOnly": {"allowedOrigins": self.readonly_origins},
                    "writable": {"allowedOrigins": self.writable_origins},
                    "merged": {"allowedOrigins": self.readonly_origins + self.writable_origins}}
        if path.startswith("/resource-servers/") and ("/resources" in path or path.endswith("/actions?limit=100&offset=0")):
            return self.resource_tree(path)
        # Users are (name, type[, ouId in the list, ouId in the user's details]);
        # None leaves the field out. Like ThunderID 1.0, application lists omit
        # ouId; runtime apps are names or (name, ouId in the app's details).
        if path == "/users?limit=100&offset=0":
            users = []
            for name, t, *ous in self.users:
                user = {"id": "u-" + name, "type": t, "attributes": {"username": name}}
                if (ous[0] if ous else "real-existing-ou") is not None:
                    user["ouId"] = ous[0] if ous else "real-existing-ou"
                users.append(user)
            return {"users": users}
        if path.startswith("/users/u-") and method is None:
            for name, t, *ous in self.users:
                if path == "/users/u-" + name:
                    ou = ous[1] if len(ous) > 1 else (ous[0] if ous and ous[0] else "real-existing-ou")
                    return {"id": "u-" + name, "type": t, **({"ouId": ou} if ou else {})}
        if path == "/applications?limit=100&offset=0":
            return {"applications": [{"id": "id-" + app_name(a), "name": app_name(a)} for a in self.runtime_apps]}
        if path.startswith("/applications/id-") and method is None:
            for a in self.runtime_apps:
                if path == "/applications/id-" + app_name(a):
                    ou = a[1] if isinstance(a, tuple) else "real-existing-ou"
                    return {"id": "id-" + app_name(a), "name": app_name(a), **({"ouId": ou} if ou else {})}
        if path == "/user-types?limit=100":
            return {"types": [{"name": name, "ouId": "real-existing-ou"} for name in self.user_types]}
        if path == "/import":
            count = len(list(yaml.safe_load_all(payload["content"])))
            return {"summary": {"totalDocuments": count, "imported": count, "failed": int(self.failed)},
                    "results": [{"status": "success", "operation": "create"} for _ in range(count)]}
        if missing_ok:
            return {"id": "existing"} if self.existing else None
        raise AssertionError(path)


class IdentityTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.path = Path(self.directory.name)
        self.config_path = self.path / "config.yaml"
        self.config_path.write_text(yaml.safe_dump(config()))
        # Includes YAML syntax and template characters: values must survive as
        # literal credentials, never become YAML keys or additional documents.
        self.secret = "a" * 32 + ': # literal\n---\n${NOT_A_VARIABLE}'
        self.env = patch.dict(os.environ, {key: self.secret for key in provision.SECRET_NAMES})
        self.env.start()
        self.addCleanup(self.env.stop)
        self.output = self.path / "rendered"
        provision.render(self.config_path, self.output)
        self.cfg, self.docs = provision.load_bundle(self.output)

    def test_only_amp_resources_and_existing_ou(self):
        self.assertEqual(len(self.docs), 18)
        for doc in self.docs:
            self.assertEqual(doc["ouId"], "real-existing-ou")
            self.assertNotIn(doc["resource_type"], ("user", "server_config", "organization_unit"))
        user_types = [d for d in self.docs if d["resource_type"] == "user_type"]
        self.assertEqual([(d["id"], d["name"]) for d in user_types], [("engineer", "engineer")])
        self.assertTrue(user_types[0]["schema"]["password"]["credential"])

    def test_console_user_type_must_be_allowed_to_sign_in(self):
        config = yaml.safe_load(self.config_path.read_text())
        config["allowedUserTypes"] = ["openchoreo-user"]
        with self.assertRaisesRegex(provision.ProvisionError, "Add engineer to allowedUserTypes"):
            provision.variables_for(config)

    def test_readiness_requires_console_user_type(self):
        with self.assertRaisesRegex(provision.ProvisionError, "no engineer user type"):
            provision.check_readiness(FakeThunder(user_types=("openchoreo-user",)), self.cfg)
        self.assertNotIn("01900000-0000-7000-8000-000000000001", (self.output / "identities.yaml").read_text())

    def test_clients_share_credentials_with_core_and_use_existing_users(self):
        core = yaml.safe_load((self.output / "agent-manager.yaml").read_text())
        clients = {d["id"]: d for d in self.docs if d["resource_type"] == "application"}
        self.assertEqual(len(clients), 8)
        self.assertEqual(core["agentManagerService"]["config"]["oidc"]["clientSecret"], self.secret)
        self.assertEqual(clients["amp-api-client"]["inboundAuthConfig"][0]["config"]["clientSecret"], self.secret)
        console = clients["amp-console-client"]
        self.assertEqual(console["allowedUserTypes"], ["openchoreo-user", "engineer"])
        self.assertEqual(console["authFlowId"], "existing-flow")
        self.assertNotIn("clientSecret", console["inboundAuthConfig"][0]["config"])
        system_role = next(d for d in self.docs if d["id"] == "amp-thunder-system-access")
        self.assertEqual(system_role["permissions"][0]["resourceServerId"], "existing-system-rs")

    def test_complete_scope_catalog_and_mcp_separation(self):
        clients = {d["id"]: d["inboundAuthConfig"][0]["config"] for d in self.docs if d["resource_type"] == "application"}
        catalog = set(clients["amp-api-client"]["scopes"])
        self.assertEqual(len(catalog), 106)
        observer = set(clients["am-obs-mcp-client"]["scopes"]) - {"openid", "profile", "email"}
        self.assertEqual(len(observer), 4)
        self.assertTrue(all(s.startswith("amp:observability:") for s in observer))
        self.assertFalse(observer & set(clients["am-mcp-client"]["scopes"]))
        self.assertNotIn("scopes", clients["am-observer-client"])

    def test_private_files_and_no_overwrite(self):
        self.assertEqual(self.output.stat().st_mode & 0o777, 0o700)
        for file in self.output.iterdir():
            self.assertEqual(file.stat().st_mode & 0o777, 0o600)
        with self.assertRaises(FileExistsError):
            provision.render(self.config_path, self.output)

    def test_missing_secret_and_placeholders_fail_before_writing(self):
        with patch.dict(os.environ, {"AMP_API_CLIENT_SECRET": ""}):
            with self.assertRaises(provision.ProvisionError):
                provision.render(self.config_path, self.path / "invalid")
        self.assertFalse((self.path / "invalid").exists())
        broken = config()
        broken["organizationUnitId"] = "<replace>"
        with self.assertRaises(provision.ProvisionError):
            provision.variables_for(broken)

    def test_dry_run_cannot_apply(self):
        client = FakeThunder()
        provision.import_bundle(client, self.cfg, self.docs)
        posts = [payload for path, payload in client.calls if path == "/import"]
        self.assertEqual(len(posts), 1)
        self.assertTrue(posts[0]["dryRun"])
        self.assertFalse(posts[0]["options"]["upsert"])

    def test_failed_dry_run_never_applies(self):
        client = FakeThunder(failed=True)
        with self.assertRaises(provision.ProvisionError):
            provision.import_bundle(client, self.cfg, self.docs, apply=True)
        self.assertEqual(sum(path == "/import" for path, _ in client.calls), 1)

    def test_apply_runs_dry_run_then_create_only_import(self):
        client = FakeThunder()
        provision.import_bundle(client, self.cfg, self.docs, apply=True)
        posts = [payload for path, payload in client.calls if path == "/import"]
        self.assertEqual([p["dryRun"] for p in posts], [True, False])
        self.assertTrue(all(p["options"]["upsert"] is False for p in posts))

    def test_existing_identity_blocks_import(self):
        client = FakeThunder(existing=True)
        with self.assertRaises(provision.ProvisionError):
            provision.import_bundle(client, self.cfg, self.docs, apply=True)
        self.assertFalse(any(path == "/import" for path, _ in client.calls))

    def test_wrong_issuer_blocks_import(self):
        client = FakeThunder(issuer="https://other.company.test")
        with self.assertRaises(provision.ProvisionError):
            provision.import_bundle(client, self.cfg, self.docs, apply=True)
        self.assertFalse(any(path == "/import" for path, _ in client.calls))

    def test_shared_default_is_checked_without_mutation(self):
        client = FakeThunder(default="urn:existing:platform")
        with self.assertRaisesRegex(provision.ProvisionError, "Shared default"):
            provision.check_readiness(client, self.cfg)
        self.assertTrue(all(payload is None for _, payload in client.calls))
        provision.check_readiness(FakeThunder(), self.cfg)

    def test_core_values_select_tls_and_keep_chart_audience(self):
        core = yaml.safe_load((self.output / "agent-manager.yaml").read_text())
        self.assertIs(core["agentManagerService"]["config"]["tlsEnabled"], True)
        self.assertEqual(core["console"]["config"]["tlsEnabled"], "true")
        self.assertNotIn("audience", core["agentManagerService"]["config"]["keyManager"])
        plain = config()
        plain["apiPublicUrl"] = "http://api.company.test"
        variables = provision.variables_for(plain)
        self.assertIs(variables["TLS_ENABLED"], False)
        self.assertEqual(variables["TLS_ENABLED_TEXT"], "false")

    def test_shared_settings_report_without_apply(self):
        client = FakeThunder(default=None, writable_origins=(), amp_resource_server=True)
        provision.configure_shared_settings(client, self.cfg)
        self.assertTrue(all(payload is None for _, payload in client.calls))

    def test_shared_settings_set_missing_default_and_append_origin(self):
        regex = {"regex": "^https://.*\\.openchoreo\\.company\\.test$"}
        client = FakeThunder(default=None, writable_origins=("https://portal.company.test", regex),
                             amp_resource_server=True)
        provision.configure_shared_settings(client, self.cfg, apply=True)
        writes = {path: payload for path, payload in client.calls if payload is not None}
        self.assertEqual(writes["/server-config/defaultResourceServer"], {"resourceServerId": "amp-resource-server"})
        # Existing writable entries, including regex objects, are kept in order.
        self.assertEqual(writes["/server-config/cors"]["allowedOrigins"],
                         ["https://portal.company.test", regex, "https://amp.company.test"])

    def test_shared_settings_never_replace_another_default(self):
        client = FakeThunder(default="urn:existing:platform", writable_origins=())
        with self.assertRaisesRegex(provision.ProvisionError, "different default"):
            provision.configure_shared_settings(client, self.cfg, apply=True)
        self.assertTrue(all(payload is None for _, payload in client.calls))

    def test_shared_settings_require_imported_resource_server(self):
        client = FakeThunder(default=None)
        with self.assertRaisesRegex(provision.ProvisionError, "Import the identity bundle"):
            provision.configure_shared_settings(client, self.cfg, apply=True)
        self.assertTrue(all(payload is None for _, payload in client.calls))

    def test_shared_settings_already_met_changes_nothing(self):
        client = FakeThunder()
        provision.configure_shared_settings(client, self.cfg, apply=True)
        self.assertTrue(all(payload is None for _, payload in client.calls))

    def test_remove_reports_without_writing(self):
        client = FakeThunder(existing=True, writable_default="amp-resource-server",
                             users=[("mark", "engineer"), ("alice", "openchoreo-user")],
                             runtime_apps=["amp-publisher-ou", "Console"])
        output = io.StringIO()
        with redirect_stdout(output):
            provision.remove_identities(client, self.cfg, self.docs)
        text = output.getvalue()
        self.assertIn("Would clear the default resource server", text)
        self.assertIn("Would remove the AMP console origin", text)
        self.assertIn("Would delete user mark (type engineer)", text)
        self.assertNotIn("alice", text)
        self.assertIn("application amp-publisher-ou", text)
        self.assertNotIn("application Console", text)
        self.assertTrue(all(method is None for method, _ in client.methods))

    def test_remove_deletes_only_users_and_apps_confirmed_in_the_organization_unit(self):
        client = FakeThunder(existing=True, users=[
            ("mark", "engineer"),                                  # ouId in the list
            ("nina", "engineer", None, "real-existing-ou"),        # ouId only in the details
            ("omar", "engineer", "another-ou"),                    # another unit
            ("pia", "engineer", None, None),                       # unit unknown
        ], runtime_apps=["amp-publisher-ou", ("amp-scheduler-other", "another-ou"), ("amp-publisher-unknown", None)])
        _, deletions = provision.removal_plan(client, self.cfg, self.docs)
        paths = [path for path, _ in deletions]
        self.assertIn("/users/u-mark", paths)
        self.assertIn("/users/u-nina", paths)
        self.assertNotIn("/users/u-omar", paths)
        self.assertNotIn("/users/u-pia", paths)
        self.assertIn("/applications/id-amp-publisher-ou", paths)
        self.assertNotIn("/applications/id-amp-scheduler-other", paths)
        self.assertNotIn("/applications/id-amp-publisher-unknown", paths)

    def test_remove_apply_reverts_settings_then_deletes_in_dependency_order(self):
        client = FakeThunder(existing=True, writable_default="amp-resource-server", users=[("mark", "engineer")],
                             runtime_apps=["amp-scheduler-ou"], amp_resource_server=True)
        with redirect_stdout(io.StringIO()):
            provision.remove_identities(client, self.cfg, self.docs, apply=True)
        writes = [(m, p) for m, p in client.methods if m in ("PUT", "DELETE")]
        first_delete = next(i for i, (m, _) in enumerate(writes) if m == "DELETE")
        self.assertTrue(all(m == "PUT" for m, _ in writes[:first_delete]))
        self.assertIn(("PUT", "/server-config/defaultResourceServer"), writes[:first_delete])
        deleted = [p for m, p in writes if m == "DELETE"]
        self.assertLess(deleted.index("/users/u-mark"), deleted.index("/user-types/engineer"))
        self.assertLess(deleted.index("/roles/amp-role-admin"), deleted.index("/resource-servers/amp-resource-server"))
        self.assertLess(deleted.index("/groups/amp-admins"), deleted.index("/applications/amp-console-client"))
        self.assertIn("/applications/id-amp-scheduler-ou", deleted)
        top_level = [p for p in deleted if not p.startswith(("/users/", "/applications/id-"))
                     and "/resources/" not in p and "/actions/" not in p]
        self.assertEqual(len(top_level), 18)

    def test_remove_empties_resource_servers_bottom_up(self):
        client = FakeThunder(existing=True, amp_resource_server=True)
        with redirect_stdout(io.StringIO()):
            provision.remove_identities(client, self.cfg, self.docs, apply=True)
        deleted = [p for m, p in client.methods if m == "DELETE"]
        base = "/resource-servers/amp-resource-server"
        order = [base + "/resources/r2/actions/a2",
                 base + "/resources/r2", base + "/resources/r1/actions/a1", base + "/resources/r1",
                 base + "/actions/s1", base]
        self.assertEqual([p for p in deleted if p.startswith(base)], order)

    def test_remove_keeps_a_default_it_did_not_set(self):
        client = FakeThunder(existing=True, writable_default="platform-default")
        with redirect_stdout(io.StringIO()):
            provision.remove_identities(client, self.cfg, self.docs, apply=True)
        self.assertNotIn(("PUT", "/server-config/defaultResourceServer"), client.methods)

    def test_remove_needs_only_the_non_secret_configuration(self):
        config, documents = provision.load_removal_inputs(self.config_path)
        self.assertEqual(config["organizationUnitId"], "real-existing-ou")
        self.assertEqual({(d["resource_type"], d["id"]) for d in documents},
                         {(d["resource_type"], d["id"]) for d in self.docs})

    def test_remove_with_nothing_left_changes_nothing(self):
        client = FakeThunder(existing=False, writable_origins=("https://openchoreo.company.test",))
        output = io.StringIO()
        with redirect_stdout(output):
            provision.remove_identities(client, self.cfg, self.docs, apply=True)
        self.assertIn("Nothing to remove", output.getvalue())
        self.assertFalse(any(m in ("PUT", "DELETE") for m, _ in client.methods))

    def test_failed_resource_result_is_not_success(self):
        result = {"summary": {"totalDocuments": 1, "imported": 1, "failed": 0},
                  "results": [{"status": "failed", "operation": "create"}]}
        with self.assertRaises(provision.ProvisionError):
            provision.validate_result(result, 1)

    def test_ca_file_is_trusted_for_https(self):
        with self.assertRaisesRegex(provision.ProvisionError, "does not exist"):
            provision.Thunder("https://id.company.test", "token", ca_file=str(self.path / "missing.pem"))
        loaded = []
        with patch.object(provision.ssl.SSLContext, "load_verify_locations",
                          lambda context, cafile=None, **_: loaded.append(cafile)):
            ca = self.path / "ca.pem"
            ca.write_text("placeholder")
            client = provision.Thunder("https://id.company.test", "token", ca_file=str(ca))
        self.assertEqual(loaded, [str(ca)])
        handlers = [h for h in client.opener.handlers if isinstance(h, provision.HTTPSHandler)]
        self.assertTrue(handlers and handlers[0]._context.verify_mode == provision.ssl.CERT_REQUIRED)

    def test_remove_rejects_the_unfilled_template(self):
        template = self.path / "template.yaml"
        template.write_text((ROOT / "values/thunder-identities.yaml").read_text())
        with self.assertRaises(provision.ProvisionError) as error:
            provision.load_removal_inputs(template)
        self.assertIn("unfilled template", str(error.exception))

    def test_missing_bundle_names_the_file(self):
        missing = self.path / "not-rendered"
        stderr = io.StringIO()
        argv = ["provision", "dry-run", "--bundle-dir", str(missing), "--thunder-url", "https://id.company.test"]
        with patch.object(provision.sys, "argv", argv), patch.object(provision.sys, "stderr", stderr):
            self.assertEqual(provision.main(), 1)
        self.assertIn(str(missing / "deployment.json"), stderr.getvalue())
        self.assertIn("Render the bundle", stderr.getvalue())

    def test_http_and_redirects_do_not_leak_credentials(self):
        with self.assertRaises(provision.ProvisionError):
            provision.Thunder("http://id.company.test", "token")
        client = provision.Thunder("https://id.company.test", "secret-token")
        with patch.object(client.opener, "open", side_effect=HTTPError("url", 302, "secret-token", {}, None)):
            with self.assertRaises(provision.ProvisionError) as error:
                client.request("/import", {})
            self.assertNotIn("secret-token", str(error.exception))
        self.assertIsNone(provision.NoRedirect().redirect_request(None, None, 302, "", {}, "https://other.test"))


if __name__ == "__main__":
    unittest.main()
