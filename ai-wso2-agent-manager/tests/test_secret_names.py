# Copyright 2026 The OpenChoreo Authors
# SPDX-License-Identifier: Apache-2.0
import importlib.util
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("names", ROOT / "scripts/amp-secret-names.py")
names = importlib.util.module_from_spec(spec)
spec.loader.exec_module(names)

MONITOR = "monitor-faccf424-9b9e-4011-aa14-46f14ea5dd2b-secrets"


class SecretNameTests(unittest.TestCase):
    def decide(self, candidates, labelled=(), references=(), agents=("scout-agent", "orders"),
               environments=("development",)):
        return dict((name, action) for action, name in names.classify(
            set(candidates), set(labelled), set(references), set(agents), set(environments)))

    def test_agent_manager_leftovers_are_deleted(self):
        leftovers = [
            "scout-agent-default-secrets",
            "scout-agent-development-secrets",
            "scout-agent-agent-api-key-default-secrets",
            "scout-agent-agent-identity-development-secrets",
            "deepwiki-default-deepwiki-88b3ec47516eb878-proxy-secrets",
            "llm-agent-default-llm-agent-d591c110746d5e1b-7f09ab2c-proxy-sec",
            "amp-publisher-ou-secrets",
            MONITOR,
        ]
        self.assertEqual(self.decide(leftovers), {name: "delete" for name in leftovers})

    def test_agents_are_also_recognized_from_their_credential_secrets(self):
        decided = self.decide(["old-agent-agent-api-key-default-secrets", "old-agent-default-secrets"], agents=())
        self.assertEqual(decided["old-agent-default-secrets"], "delete")

    def test_labelled_references_are_deleted_whatever_their_name(self):
        self.assertEqual(self.decide(["custom-name"], labelled=["custom-name"], references=["custom-name"]),
                         {"custom-name": "delete"})

    def test_other_products_secrets_are_kept(self):
        decided = self.decide(["orders-proxy-secrets", "orders-db-secrets", "github-credentials",
                               "billing-development-secrets"])
        self.assertEqual(decided.get("orders-proxy-secrets"), "unconfirmed")
        self.assertEqual(decided.get("orders-db-secrets"), "unconfirmed")
        self.assertNotIn("github-credentials", decided)
        self.assertNotIn("billing-development-secrets", decided)
        self.assertNotIn("delete", decided.values())

    def test_an_unlabelled_reference_with_an_agent_manager_name_is_kept(self):
        decided = self.decide(["scout-agent-default-secrets", MONITOR],
                              references=["scout-agent-default-secrets", MONITOR])
        self.assertEqual(decided, {"scout-agent-default-secrets": "unconfirmed", MONITOR: "unconfirmed"})


if __name__ == "__main__":
    unittest.main()
