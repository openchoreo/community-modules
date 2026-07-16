#!/bin/bash
# Copyright 2026 The OpenChoreo Authors
# SPDX-License-Identifier: Apache-2.0

## NOTE
# This script is executed as a Helm pre-install/upgrade Job. All commands must
# be idempotent — the Job can run multiple times across the lifetime of a release.

set -euo pipefail

AWS_REGION="${AWS_REGION:?AWS_REGION is required}"
LOG_GROUP_PREFIX="${LOG_GROUP_PREFIX:-/aws/containerinsights}"
LOG_GROUP_NAME="${LOG_GROUP_NAME:-}"
RETENTION_DAYS="${RETENTION_DAYS:-7}"
EVENTS_PROVISION_LOG_GROUP="${EVENTS_PROVISION_LOG_GROUP:-false}"
EVENTS_LOG_GROUP_NAME="${EVENTS_LOG_GROUP_NAME:-}"
EVENTS_RETENTION_DAYS="${EVENTS_RETENTION_DAYS:-30}"

# CloudWatch's PutRetentionPolicy only accepts this fixed set of values; any
# other number is rejected by the API mid-run, leaving log groups created but
# unconfigured. Fail fast with a clearer message instead.
validate_retention() {
  case " 1 3 5 7 14 30 60 90 120 150 180 365 400 545 731 1096 1827 2192 2557 2922 3288 3653 " in
    *" ${1} "*) ;;
    *)
      echo "Retention '${1}' is not a valid CloudWatch retention." >&2
      echo "Allowed: 1, 3, 5, 7, 14, 30, 60, 90, 120, 150, 180, 365, 400, 545, 731, 1096, 1827, 2192, 2557, 2922, 3288, 3653" >&2
      exit 1
      ;;
  esac
}

validate_retention "${RETENTION_DAYS}"

# Strip any trailing slash so joining the segments stays predictable.
LOG_GROUP_PREFIX="${LOG_GROUP_PREFIX%/}"
LOG_GROUP_NAME="${LOG_GROUP_NAME:-${LOG_GROUP_PREFIX}/application}"
EVENTS_LOG_GROUP_NAME="${EVENTS_LOG_GROUP_NAME:-${LOG_GROUP_PREFIX}/events}"

# Each entry is "<log-group-name> <retention-days>". The application log group is
# always provisioned; the events log group is opt-in
# (events.provisionLogGroup) and gets its own, typically longer, retention.
LOG_GROUPS=(
  "${LOG_GROUP_NAME} ${RETENTION_DAYS}"
)

if [ "${EVENTS_PROVISION_LOG_GROUP}" = "true" ]; then
  validate_retention "${EVENTS_RETENTION_DAYS}"
  LOG_GROUPS+=("${EVENTS_LOG_GROUP_NAME} ${EVENTS_RETENTION_DAYS}")
fi

echo "Ensuring CloudWatch log groups exist in region ${AWS_REGION}"

for entry in "${LOG_GROUPS[@]}"; do
  # Split "<group> <retention>" into its two fields.
  group="${entry%% *}"
  retention="${entry##* }"
  echo "Ensuring log group: ${group} (retention ${retention}d)"

  # Attempt creation directly and key off the API's own "already exists" error.
  # This avoids a describe→create TOCTOU race and stops a real failure
  # (permissions, throttling, wrong region) from being mistaken for "not found".
  set +e
  create_output=$(aws logs create-log-group \
    --region "${AWS_REGION}" \
    --log-group-name "${group}" 2>&1)
  create_status=$?
  set -e

  if [ "${create_status}" -eq 0 ]; then
    echo "Created log group ${group}"
  elif printf '%s' "${create_output}" | grep -q "ResourceAlreadyExistsException"; then
    echo "Log group ${group} already exists"
  else
    echo "Failed to create log group ${group}: ${create_output}" >&2
    exit "${create_status}"
  fi

  echo "Setting retention on ${group} to ${retention} days"
  aws logs put-retention-policy \
    --region "${AWS_REGION}" \
    --log-group-name "${group}" \
    --retention-in-days "${retention}"
done

echo "CloudWatch log groups are ready"
