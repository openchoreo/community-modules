#!/bin/bash
# Copyright 2026 The OpenChoreo Authors
# SPDX-License-Identifier: Apache-2.0

## NOTE
# Please ensure that any commands in this script are idempotent as the script may run multiple times

# Read configuration from environment variables
OPENOBSERVE_PASSWORD="${OPENOBSERVE_PASSWORD}"
OPENOBSERVE_USERNAME="${OPENOBSERVE_USERNAME}"

OPENOBSERVE_URL="http://openobserve:5080"


# 1. Check OpenObserve status and wait for it to become ready. Any API calls to configure
#    OpenObserve should be made only after the it is deemed ready by this API.

MAX_RETRIES=30
RETRY_INTERVAL=10

echo "Checking OpenObserve health status..."

HEALTHY=false
for i in $(seq 1 $MAX_RETRIES); do
  echo "Attempt $i/$MAX_RETRIES: Checking OpenObserve at $OPENOBSERVE_URL/healthz"

  # Make health check request
  RESPONSE=$(curl -s -w "\n%{http_code}" "$OPENOBSERVE_URL/healthz" 2>/dev/null)
  HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
  BODY=$(echo "$RESPONSE" | head -n-1)

  if [ "$HTTP_CODE" = "200" ]; then
    # Check if response contains expected status
    if echo "$BODY" | grep -q '"status"[[:space:]]*:[[:space:]]*"ok"'; then
      echo "OpenObserve is healthy and ready!"
      HEALTHY=true
      break
    else
      echo "OpenObserve responded but status is not ok. Response: $BODY"
    fi
  else
    echo "OpenObserve not ready yet (HTTP $HTTP_CODE). Retrying in $RETRY_INTERVAL seconds..."
  fi

  sleep $RETRY_INTERVAL
done

if [ "$HEALTHY" != "true" ]; then
  echo "ERROR: OpenObserve did not become healthy after $MAX_RETRIES attempts"
  exit 1
fi



## 2. Create an alert template in OpenObserve (required before creating a destination)

OPENOBSERVE_ORG="default"

TEMPLATE_NAME="openchoreo_alerts_template"

echo "Configuring alert template..."

# Check if template already exists
echo "Checking if template '$TEMPLATE_NAME' already exists..."
EXISTING_TEMPLATES=$(curl -s -u "$OPENOBSERVE_USERNAME:$OPENOBSERVE_PASSWORD" \
  "$OPENOBSERVE_URL/api/$OPENOBSERVE_ORG/alerts/templates")

if echo "$EXISTING_TEMPLATES" | grep -q "\"name\"[[:space:]]*:[[:space:]]*\"$TEMPLATE_NAME\""; then
  echo "Template '$TEMPLATE_NAME' already exists. Skipping creation."
else
  echo "Creating alert template '$TEMPLATE_NAME'..."

  CREATE_RESPONSE=$(curl -s -w "\n%{http_code}" -u "$OPENOBSERVE_USERNAME:$OPENOBSERVE_PASSWORD" \
    -X POST "$OPENOBSERVE_URL/api/$OPENOBSERVE_ORG/alerts/templates" \
    -H "Content-Type: application/json" \
    -d "{
      \"name\": \"$TEMPLATE_NAME\",
      \"body\": \"{alert_name} {alert_type} {org_name} {stream_name}\",
      \"type\": \"http\"
    }")

  HTTP_CODE=$(echo "$CREATE_RESPONSE" | tail -n1)
  BODY=$(echo "$CREATE_RESPONSE" | head -n-1)

  if [ "$HTTP_CODE" = "200" ] || [ "$HTTP_CODE" = "201" ]; then
    echo "Alert template created successfully!"
  else
    echo "ERROR: Failed to create alert template (HTTP $HTTP_CODE). Response: $BODY"
    exit 1
  fi
fi


## 3. Create a webhook based alert destination in OpenObserve

DESTINATION_NAME="openchoreo_alerts"
WEBHOOK_URL="http://logs-adapter:9098/api/alerting/webhook"

echo "Configuring webhook based alert destination..."

# Check if destination already exists
echo "Checking if destination '$DESTINATION_NAME' already exists..."
EXISTING_DESTINATIONS=$(curl -s -u "$OPENOBSERVE_USERNAME:$OPENOBSERVE_PASSWORD" \
  "$OPENOBSERVE_URL/api/$OPENOBSERVE_ORG/alerts/destinations")

if echo "$EXISTING_DESTINATIONS" | grep -q "\"name\"[[:space:]]*:[[:space:]]*\"$DESTINATION_NAME\""; then
  echo "Destination '$DESTINATION_NAME' already exists. Skipping creation."
else
  echo "Creating webhook based alert destination '$DESTINATION_NAME'..."

  CREATE_RESPONSE=$(curl -s -w "\n%{http_code}" -u "$OPENOBSERVE_USERNAME:$OPENOBSERVE_PASSWORD" \
    -X POST "$OPENOBSERVE_URL/api/$OPENOBSERVE_ORG/alerts/destinations" \
    -H "Content-Type: application/json" \
    -d "{
      \"name\": \"$DESTINATION_NAME\",
      \"url\": \"$WEBHOOK_URL\",
      \"method\": \"post\",
      \"type\": \"http\",
      \"template\": \"$TEMPLATE_NAME\",
      \"skip_tls_verify\": false,
      \"headers\": {
        \"Content-Type\": \"application/json\"
      }
    }")

  HTTP_CODE=$(echo "$CREATE_RESPONSE" | tail -n1)
  BODY=$(echo "$CREATE_RESPONSE" | head -n-1)

  if [ "$HTTP_CODE" = "200" ] || [ "$HTTP_CODE" = "201" ]; then
    echo "Webhook based alert destination created successfully!"
  else
    echo "ERROR: Failed to create webhook based alert destination (HTTP $HTTP_CODE). Response: $BODY"
    exit 1
  fi
fi

echo "OpenObserve configuration completed successfully!"
