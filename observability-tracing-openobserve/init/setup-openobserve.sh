#!/bin/bash
# Copyright 2026 The OpenChoreo Authors
# SPDX-License-Identifier: Apache-2.0

## NOTE
# Every step must be idempotent: the script runs on every install and upgrade.

set -uo pipefail

OPENOBSERVE_URL="${OPENOBSERVE_URL}"
OPENOBSERVE_ORG="${OPENOBSERVE_ORG}"
# normalizeEmail trims surrounding whitespace, newlines included, and lowercases the whole value, as OpenObserve does
# when it creates a user and as the adapter does.
normalizeEmail() {
  local v="$1"
  v="${v#"${v%%[![:space:]]*}"}"
  v="${v%"${v##*[![:space:]]}"}"
  printf '%s' "${v,,}"
}

# The root credentials are needed only to set up the raw query user, so their Secret is optional.
OPENOBSERVE_USERNAME="${OPENOBSERVE_USERNAME:-}"
OPENOBSERVE_PASSWORD="${OPENOBSERVE_PASSWORD:-}"

# Every request is bounded, so a connection OpenObserve accepts but never answers fails the step instead of holding
# up the Helm hook.
CURL_TIMEOUTS=(--connect-timeout 5 --max-time 30)

# 1. The user raw trace queries run as.
# The credentials come from the rawQueryUsername and rawQueryPassword keys of their own Secret
# (adapter.rawQuery.credentialsSecretName). Without them the user is not created and the adapter answers raw queries
# with 501; the rest of the module works as before.
# Open-source OpenObserve has no read-only role, so the user is an admin of the organization: it is kept apart from the
# root user, and the adapter only sends it searches of trace streams.
# This is checked before waiting for OpenObserve, so an install without the Secret doesn't depend on OpenObserve.

rawQueryUsername=$(normalizeEmail "${RAW_QUERY_USERNAME:-}")
rawQueryPassword="${RAW_QUERY_PASSWORD:-}"
rawQuerySecret="${RAW_QUERY_CREDENTIALS_SECRET:-openobserve-traces-users}"
if [ -z "$rawQueryUsername" ] && [ -z "$rawQueryPassword" ]; then
  echo "Skipping the raw trace query user: the Secret $rawQuerySecret is missing or has no rawQueryUsername and" \
    "rawQueryPassword keys, so raw trace queries are not supported"
  exit 0
fi
if [ -z "$rawQueryUsername" ] || [ -z "$rawQueryPassword" ]; then
  echo "Skipping the raw trace query user: the Secret $rawQuerySecret must have both the rawQueryUsername and" \
    "rawQueryPassword keys"
  exit 0
fi

if [ -z "$OPENOBSERVE_USERNAME" ] || [ -z "$OPENOBSERVE_PASSWORD" ]; then
  echo "ERROR: the root credentials are needed to set up the raw trace query user, but the Secret" \
    "${OPENOBSERVE_CREDENTIALS_SECRET:-openobserve-admin-credentials} is missing or doesn't have them" \
    "(openObserveSetup.openObserveCredentialsSecret)"
  exit 1
fi
rootUsername=$(normalizeEmail "$OPENOBSERVE_USERNAME")
if [ "$rawQueryUsername" = "$rootUsername" ]; then
  echo "ERROR: rawQueryUsername in the Secret $rawQuerySecret is the root user; raw trace queries need a user of their own"
  exit 1
fi

# 2. Wait for OpenObserve to become ready.

MAX_RETRIES=30
RETRY_INTERVAL=10

echo "Checking OpenObserve health status..."
healthy=false
for i in $(seq 1 "$MAX_RETRIES"); do
  # -f fails the request on an HTTP error, so only a 200 with status ok counts.
  if status=$(curl -s -f "${CURL_TIMEOUTS[@]}" "$OPENOBSERVE_URL/healthz" | jq -r '.status // empty' 2>/dev/null) &&
    [ "$status" = "ok" ]; then
    echo "OpenObserve is healthy and ready"
    healthy=true
    break
  fi
  echo "Attempt $i/$MAX_RETRIES: OpenObserve is not ready yet. Retrying in $RETRY_INTERVAL seconds..."
  sleep "$RETRY_INTERVAL"
done
if [ "$healthy" != "true" ]; then
  echo "ERROR: OpenObserve did not become healthy after $MAX_RETRIES attempts"
  exit 1
fi

# 3. Create the user, or set its password if it exists.

users=$(curl -s -f "${CURL_TIMEOUTS[@]}" -u "$OPENOBSERVE_USERNAME:$OPENOBSERVE_PASSWORD" "$OPENOBSERVE_URL/api/$OPENOBSERVE_ORG/users")
if [ $? -ne 0 ]; then
  echo "ERROR: failed to list the users of the organization $OPENOBSERVE_ORG"
  exit 1
fi

if echo "$users" | jq -e --arg email "$rawQueryUsername" '.data[] | select(.email == $email)' >/dev/null; then
  echo "Updating the raw trace query user $rawQueryUsername..."
  method=PUT
  url="$OPENOBSERVE_URL/api/$OPENOBSERVE_ORG/users/$rawQueryUsername"
  payload=$(jq -n --arg password "$rawQueryPassword" \
    '{change_password: true, new_password: $password, role: "admin"}')
else
  echo "Creating the raw trace query user $rawQueryUsername..."
  method=POST
  url="$OPENOBSERVE_URL/api/$OPENOBSERVE_ORG/users"
  payload=$(jq -n --arg email "$rawQueryUsername" --arg password "$rawQueryPassword" \
    '{email: $email, password: $password, role: "admin", first_name: "OpenChoreo", last_name: "raw trace queries"}')
fi

response=$(curl -s "${CURL_TIMEOUTS[@]}" -w "\n%{http_code}" -u "$OPENOBSERVE_USERNAME:$OPENOBSERVE_PASSWORD" -X "$method" "$url" \
  -H "Content-Type: application/json" -d "$payload")
httpCode=$(echo "$response" | tail -n1)
body=$(echo "$response" | sed '$d')
if [ "$httpCode" != "200" ]; then
  # OpenObserve's message says why, for example a password that doesn't meet its rules.
  echo "ERROR: failed to set up the raw trace query user (HTTP $httpCode): $body"
  exit 1
fi
echo "The raw trace query user $rawQueryUsername is set up"
