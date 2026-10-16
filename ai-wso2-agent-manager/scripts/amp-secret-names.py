#!/usr/bin/env python3
# Copyright 2026 The OpenChoreo Authors
# SPDX-License-Identifier: Apache-2.0
"""Decide which secret names uninstall.sh may delete as Agent Manager's.

Reads candidate names on stdin: SecretReference, PushSecret, and OpenBao key
names from the namespace Agent Manager uses. Prints one line per name it
recognizes, "delete <name>" or "unconfirmed <name>". Other products keep their
secrets in the same places, so a name is deleted only when it is shown to be
Agent Manager's:

- its SecretReference carries managed-by=amp-agent-manager, which Agent Manager
  sets on every SecretReference it creates, or
- it has no SecretReference, as an agent's or proxy's leftovers do after the
  agent or proxy is deleted, and its name follows a format only Agent Manager
  uses.

A name that only resembles Agent Manager's is "unconfirmed", and is kept.

Environment: LABELLED (labelled SecretReference names), REFERENCES (all
SecretReference names), AGENTS (Agent Manager's agent names), ENVIRONMENTS
(environment names).
"""
import os
import re
import sys

UUID = r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}"
# Agent Manager trims names to 63 characters, which can cut the suffix short.
PROXY_SUFFIX = r"-proxy(-s(e(c(r(e(ts?)?)?)?)?)?)?"
AGENT_CREDENTIAL = re.compile(r"^(.+)-agent-(api-key|identity)-[a-z0-9-]+-secrets$")
DISTINCTIVE = [
    AGENT_CREDENTIAL,
    re.compile(r"^amp-(publisher|scheduler)-.+-secrets$"),
    re.compile(r"^monitor-" + UUID + r"-secrets$"),
    # MCP and LLM proxies: a 16-character fingerprint, then for LLM proxies an
    # 8-character suffix.
    re.compile(r"^.+-[0-9a-f]{16}(-[0-9a-f]{8})?" + PROXY_SUFFIX + r"$"),
]
# What earlier versions of this module matched, now only reported.
RESEMBLING = re.compile(r"^(.+-proxy-sec(rets)?|amp-.+-secrets|.+-agent-.+-secrets)$")


def words(name):
    return set(os.environ.get(name, "").split())


def classify(names, labelled, references, agents, environments):
    agents = set(agents)
    agents.update(m.group(1) for m in map(AGENT_CREDENTIAL.match, names) if m)
    environments = set(environments) | {"default"}
    # An agent's environment variables: <agent>-<env>-secrets, or with a
    # configuration name, <config>-<env>-<agent>-secrets.
    agent_secret = {f"{a}-{e}-secrets" for a in agents for e in environments}
    config_secret = [re.compile(r"^.+-" + re.escape(e) + "-" + re.escape(a) + r"-secrets$")
                     for a in agents for e in environments]
    result = []
    for name in sorted(names):
        recognized = (any(p.match(name) for p in DISTINCTIVE) or name in agent_secret
                      or any(p.match(name) for p in config_secret))
        resembling = recognized or RESEMBLING.match(name) or (
            name.endswith("-secrets") and any(name.startswith(a + "-") for a in agents))
        if name in labelled:
            result.append(("delete", name))
        elif name in references:
            # Agent Manager labels every SecretReference it creates.
            if resembling:
                result.append(("unconfirmed", name))
        elif recognized:
            result.append(("delete", name))
        elif resembling:
            result.append(("unconfirmed", name))
    return result


def main():
    names = {line.strip() for line in sys.stdin if line.strip()}
    for action, name in classify(names, words("LABELLED"), words("REFERENCES"),
                                 words("AGENTS"), words("ENVIRONMENTS")):
        print(action, name)


if __name__ == "__main__":
    main()
