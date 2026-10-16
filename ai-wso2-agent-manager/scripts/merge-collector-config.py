#!/usr/bin/env python3
# Copyright 2026 The OpenChoreo Authors
# SPDX-License-Identifier: Apache-2.0
"""Add Agent Manager trace handling to an existing OpenChoreo collector config.

Reads the tracing module's live ConfigMap on stdin and writes a new ConfigMap
on stdout. Everything already in the pipeline is kept: receivers (including
OTLP gRPC), processors such as k8sattributes and tail_sampling, exporters, and
any customized values. Only two additions are made, taken from
wso2/agent-manager amp/v1.0.0 deployments/values/oc-collector-configmap.yaml:

- the OTLP HTTP receiver keeps request metadata (include_metadata), and
- a resource/amp processor copies the x-user-* headers the Agent Manager
  gateway sets into openchoreo.dev/* resource attributes.
"""
import argparse
import sys

import yaml

PROCESSOR = "resource/amp"
AMP_ATTRIBUTES = [
    {"key": "openchoreo.dev/component-uid", "from_context": "metadata.x-user-component", "action": "upsert"},
    {"key": "openchoreo.dev/environment-uid", "from_context": "metadata.x-user-environment", "action": "upsert"},
    {"key": "openchoreo.dev/project-uid", "from_context": "metadata.x-user-project", "action": "upsert"},
    {"key": "openchoreo.dev/namespace", "from_context": "metadata.x-user-namespace", "action": "upsert"},
]


class MergeError(Exception):
    pass


class _Dumper(yaml.SafeDumper):
    pass


# Keep multi-line values, such as the relay configuration, readable in the output.
_Dumper.add_representer(str, lambda dumper, value: dumper.represent_scalar(
    "tag:yaml.org,2002:str", value, style="|" if "\n" in value else None))


def merge_relay(relay):
    config = yaml.safe_load(relay)
    if not isinstance(config, dict):
        raise MergeError("The relay configuration is not a YAML mapping.")
    try:
        pipeline = config["service"]["pipelines"]["traces"]
        receivers = config["receivers"]
    except (KeyError, TypeError):
        raise MergeError("Expected receivers and a traces pipeline; this is not the tracing module's collector config.") from None
    # Components may be named, such as otlp/ingest; match them by type. Only
    # the OTLP receivers the traces pipeline uses are changed.
    otlp = [name for name in (pipeline.get("receivers") or []) if component_type(name) == "otlp"]
    if not otlp or any(not isinstance((receivers.get(name) or {}).get("protocols"), dict) for name in otlp):
        raise MergeError("The traces pipeline has no OTLP receiver with protocols; this is not the tracing module's collector config.")
    if not any(component_type(name) == "opensearch" for name in (pipeline.get("exporters") or [])):
        raise MergeError("The traces pipeline does not export to OpenSearch. Run this against the receiving collector in the observability plane.")
    # The Agent Manager gateway exports over OTLP HTTP.
    with_http = [name for name in otlp if receivers[name]["protocols"].get("http") is not None]
    if not with_http:
        receivers[otlp[0]]["protocols"]["http"] = {"endpoint": "0.0.0.0:4318"}
        with_http = [otlp[0]]
    for name in with_http:
        receivers[name]["protocols"]["http"]["include_metadata"] = True
    processors = config.get("processors") or {}
    processors[PROCESSOR] = merge_processor(processors.get(PROCESSOR))
    config["processors"] = processors
    pipeline["processors"] = processor_order(pipeline.get("processors") or [])
    return yaml.safe_dump(config, sort_keys=False)


def component_type(name):
    """The type of a collector component ID: otlp for otlp and otlp/ingest."""
    return str(name).split("/", 1)[0]


def merge_processor(existing):
    """Add the AMP attribute actions to resource/amp, keeping any other actions it has."""
    if existing is None:
        return {"attributes": list(AMP_ATTRIBUTES)}
    if not isinstance(existing, dict) or set(existing) - {"attributes"} or not isinstance(existing.get("attributes"), list):
        raise MergeError(f"The existing {PROCESSOR} processor is not a list of attribute actions; merge it by hand.")
    keys = {a["key"] for a in AMP_ATTRIBUTES}
    kept = [a for a in existing["attributes"] if not (isinstance(a, dict) and a.get("key") in keys)]
    return {"attributes": kept + list(AMP_ATTRIBUTES)}


# Processors after which request metadata, which from_context reads, is gone:
# they regroup spans into new batches without the original client context.
CONTEXT_DROPPING = ("batch", "tail_sampling", "groupbytrace")


def processor_order(processors):
    """Place resource/amp after k8sattributes, so header values win, and before
    any processor that drops request metadata. Spans without the headers are
    untouched."""
    order = [name for name in processors if name != PROCESSOR]
    types = [component_type(name) for name in order]
    first_dropping = next((i for i, t in enumerate(types) if t in CONTEXT_DROPPING), len(order))
    if "k8sattributes" in types:
        k8s = types.index("k8sattributes")
        if k8s > first_dropping:
            raise MergeError(f"{order[first_dropping]} runs before k8sattributes in the traces pipeline, so request "
                             f"metadata is gone where {PROCESSOR} would read it. Move it after k8sattributes first.")
        position = k8s + 1
    else:
        position = 0
    order.insert(position, PROCESSOR)
    return order


def merge_configmap(source, name):
    if not isinstance(source, dict) or source.get("kind") != "ConfigMap" or "relay" not in (source.get("data") or {}):
        raise MergeError("Input must be the tracing module's collector ConfigMap, with a relay key.")
    return {
        "apiVersion": "v1",
        "kind": "ConfigMap",
        # Fresh metadata: no Helm ownership, resourceVersion, or UID from the source.
        "metadata": {
            "name": name,
            "namespace": source["metadata"]["namespace"],
            "labels": {"app.kubernetes.io/part-of": "wso2-agent-manager"},
            "annotations": {"wso2.com/merged-from": source["metadata"]["name"]},
        },
        "data": {**source["data"], "relay": merge_relay(source["data"]["relay"])},
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--name", default="amp-opentelemetry-collector-config", help="Name of the ConfigMap to write.")
    args = parser.parse_args()
    try:
        output = merge_configmap(yaml.safe_load(sys.stdin), args.name)
    except (MergeError, yaml.YAMLError) as error:
        print(str(error), file=sys.stderr)
        return 1
    except (KeyError, TypeError):
        print("Input ConfigMap is missing its metadata.", file=sys.stderr)
        return 1
    yaml.dump(output, sys.stdout, Dumper=_Dumper, sort_keys=False)
    return 0


if __name__ == "__main__":
    sys.exit(main())
