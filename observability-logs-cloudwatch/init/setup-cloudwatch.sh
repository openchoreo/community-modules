#!/bin/bash
# Copyright 2026 The OpenChoreo Authors
# SPDX-License-Identifier: Apache-2.0

## NOTE
# This script is executed as a Helm pre-install/upgrade Job. All commands must
# be idempotent — the Job can run multiple times across the lifetime of a release.

set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:?CLUSTER_NAME is required}"
AWS_REGION="${AWS_REGION:?AWS_REGION is required}"
LOG_GROUP_PREFIX="${LOG_GROUP_PREFIX:-/aws/containerinsights}"
RETENTION_DAYS="${RETENTION_DAYS:-7}"

# Strip any trailing slash so joining the segments stays predictable.
LOG_GROUP_PREFIX="${LOG_GROUP_PREFIX%/}"

LOG_GROUPS=(
  "${LOG_GROUP_PREFIX}/${CLUSTER_NAME}/application"
  "${LOG_GROUP_PREFIX}/${CLUSTER_NAME}/dataplane"
  "${LOG_GROUP_PREFIX}/${CLUSTER_NAME}/host"
)

echo "Ensuring CloudWatch log groups exist in region ${AWS_REGION} with ${RETENTION_DAYS}-day retention"

for group in "${LOG_GROUPS[@]}"; do
  echo "Checking log group: ${group}"

  existing=$(aws logs describe-log-groups \
    --region "${AWS_REGION}" \
    --log-group-name-prefix "${group}" \
    --query "logGroups[?logGroupName=='${group}'].logGroupName" \
    --output text 2>/dev/null || true)

  if [ -z "${existing}" ] || [ "${existing}" = "None" ]; then
    echo "Creating log group ${group}"
    aws logs create-log-group \
      --region "${AWS_REGION}" \
      --log-group-name "${group}"
  else
    echo "Log group ${group} already exists"
  fi

  echo "Setting retention on ${group} to ${RETENTION_DAYS} days"
  aws logs put-retention-policy \
    --region "${AWS_REGION}" \
    --log-group-name "${group}" \
    --retention-in-days "${RETENTION_DAYS}"
done

echo "CloudWatch log groups are ready"
