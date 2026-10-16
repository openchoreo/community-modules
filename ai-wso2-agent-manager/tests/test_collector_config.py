# Copyright 2026 The OpenChoreo Authors
# SPDX-License-Identifier: Apache-2.0
import copy
import importlib.util
from pathlib import Path
import unittest

import yaml

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("merge", ROOT / "scripts/merge-collector-config.py")
merge = importlib.util.module_from_spec(spec)
spec.loader.exec_module(merge)

# The tracing module's default collector ConfigMap (observability-tracing-opensearch 0.6.0).
SOURCE = next(d for d in yaml.safe_load_all((ROOT / "tests/fixtures/tracing-collector-configmap.yaml").read_text()) if d)


def relay(configmap):
    return yaml.safe_load(configmap["data"]["relay"])


class CollectorConfigTests(unittest.TestCase):
    def setUp(self):
        self.source = copy.deepcopy(SOURCE)
        self.output = merge.merge_configmap(self.source, "amp-opentelemetry-collector-config")
        self.before, self.after = relay(SOURCE), relay(self.output)

    def test_existing_receivers_processors_and_exporters_are_kept(self):
        protocols = self.after["receivers"]["otlp"]["protocols"]
        self.assertEqual(protocols["grpc"], self.before["receivers"]["otlp"]["protocols"]["grpc"])
        self.assertEqual(protocols["http"]["endpoint"], "0.0.0.0:4318")
        for name in ("k8sattributes", "tail_sampling"):
            self.assertEqual(self.after["processors"][name], self.before["processors"][name])
        self.assertEqual(self.after["exporters"], self.before["exporters"])
        self.assertEqual(self.after["extensions"], self.before["extensions"])

    def test_amp_additions(self):
        self.assertTrue(self.after["receivers"]["otlp"]["protocols"]["http"]["include_metadata"])
        attributes = self.after["processors"]["resource/amp"]["attributes"]
        self.assertEqual([a["from_context"] for a in attributes],
                         ["metadata.x-user-component", "metadata.x-user-environment",
                          "metadata.x-user-project", "metadata.x-user-namespace"])
        # Header values override k8sattributes; sampling still runs last.
        self.assertEqual(self.after["service"]["pipelines"]["traces"]["processors"],
                         ["k8sattributes", "resource/amp", "tail_sampling"])

    def test_output_is_a_new_unowned_configmap(self):
        metadata = self.output["metadata"]
        self.assertEqual(metadata["name"], "amp-opentelemetry-collector-config")
        self.assertEqual(metadata["namespace"], "openchoreo-observability-plane")
        self.assertNotIn("resourceVersion", metadata)
        self.assertFalse(any(k.startswith("meta.helm.sh") for k in metadata["annotations"]))
        self.assertNotIn("app.kubernetes.io/managed-by", metadata["labels"])
        # The source is not modified.
        self.assertEqual(self.source, SOURCE)

    def test_merge_is_idempotent(self):
        again = merge.merge_configmap(self.output, "amp-opentelemetry-collector-config")
        self.assertEqual(relay(again), self.after)

    def test_existing_resource_processor_is_not_replaced(self):
        config = copy.deepcopy(self.before)
        config["processors"]["resource"] = {"attributes": [{"key": "team", "value": "a", "action": "insert"}]}
        config["service"]["pipelines"]["traces"]["processors"].append("resource")
        source = copy.deepcopy(SOURCE)
        source["data"]["relay"] = yaml.safe_dump(config)
        merged = relay(merge.merge_configmap(source, "amp"))
        self.assertEqual(merged["processors"]["resource"], config["processors"]["resource"])
        self.assertIn("resource", merged["service"]["pipelines"]["traces"]["processors"])

    def merged_with(self, change):
        config = copy.deepcopy(self.before)
        change(config)
        source = copy.deepcopy(SOURCE)
        source["data"]["relay"] = yaml.safe_dump(config)
        return relay(merge.merge_configmap(source, "amp"))

    def test_existing_resource_amp_actions_are_kept(self):
        custom = {"key": "team", "value": "a", "action": "insert"}
        stale = {"key": "openchoreo.dev/namespace", "value": "old", "action": "insert"}
        merged = self.merged_with(lambda c: c["processors"].update({"resource/amp": {"attributes": [custom, stale]}}))
        attributes = merged["processors"]["resource/amp"]["attributes"]
        self.assertEqual(attributes[0], custom)
        self.assertEqual(attributes[1:], merge.AMP_ATTRIBUTES)

    def test_unmergeable_resource_amp_is_refused(self):
        with self.assertRaises(merge.MergeError):
            self.merged_with(lambda c: c["processors"].update({"resource/amp": {"actions": []}}))

    def test_context_dropping_processor_before_k8sattributes_is_refused(self):
        for dropping in ("batch", "tail_sampling", "batch/custom"):
            def change(c, dropping=dropping):
                c["processors"].setdefault(dropping, {})
                c["service"]["pipelines"]["traces"]["processors"] = [dropping, "k8sattributes"]
            with self.subTest(dropping=dropping), self.assertRaises(merge.MergeError):
                self.merged_with(change)

    def test_without_k8sattributes_it_runs_first(self):
        def change(c):
            c["processors"]["batch"] = {}
            c["service"]["pipelines"]["traces"]["processors"] = ["batch", "tail_sampling"]
        merged = self.merged_with(change)
        self.assertEqual(merged["service"]["pipelines"]["traces"]["processors"],
                         ["resource/amp", "batch", "tail_sampling"])

    def test_named_components_in_the_traces_pipeline_are_used(self):
        def change(c):
            c["receivers"]["otlp/ingest"] = c["receivers"].pop("otlp")
            c["receivers"]["otlp"] = {"protocols": {"http": {"endpoint": "0.0.0.0:14318"}}}  # not in the pipeline
            c["exporters"]["opensearch/traces"] = c["exporters"].pop("opensearch")
            c["service"]["pipelines"]["traces"]["receivers"] = ["otlp/ingest"]
            c["service"]["pipelines"]["traces"]["exporters"] = ["opensearch/traces"]
        merged = self.merged_with(change)
        self.assertTrue(merged["receivers"]["otlp/ingest"]["protocols"]["http"]["include_metadata"])
        self.assertNotIn("include_metadata", merged["receivers"]["otlp"]["protocols"]["http"])

    def test_non_receiving_collector_is_refused(self):
        config = copy.deepcopy(self.before)
        config["service"]["pipelines"]["traces"]["exporters"] = ["otlp"]
        source = copy.deepcopy(SOURCE)
        source["data"]["relay"] = yaml.safe_dump(config)
        with self.assertRaises(merge.MergeError):
            merge.merge_configmap(source, "amp")

    def test_unrelated_input_is_refused(self):
        with self.assertRaises(merge.MergeError):
            merge.merge_configmap({"kind": "Secret", "data": {}}, "amp")


if __name__ == "__main__":
    unittest.main()
