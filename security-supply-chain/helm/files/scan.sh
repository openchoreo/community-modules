set -eu

# Trivy reads TRIVY_SCANNERS, TRIVY_TIMEOUT, TRIVY_CACHE_DIR, TRIVY_SERVER and
# TRIVY_*DB_REPOSITORY from the environment.

json_escape() {
  printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g'
}

if [ ! -f "$IMAGE_TAR" ]; then
  echo ">> Error: Built image tar not found at $IMAGE_TAR"
  echo ">> Hint: Run this step after the build step, on the same workspace volume."
  exit 1
fi

mkdir -p "$REPORT_DIR"
STARTED_ON=$(date -u +%Y-%m-%dT%H:%M:%SZ)

set -- --input "$IMAGE_TAR" --quiet
if [ -n "$IGNORE_FILE" ]; then
  for candidate in "$IGNORE_FILE" "$IGNORE_FILE.yaml"; do
    if [ -f "$SOURCE_DIR/$candidate" ]; then
      echo ">> Applying ignore file: $candidate"
      set -- "$@" --ignorefile "$SOURCE_DIR/$candidate"
      break
    fi
  done
fi
[ -n "${TRIVY_SERVER:-}" ] && echo ">> Trivy server: $TRIVY_SERVER"

echo ">> Scanning image"
trivy image "$@" --format json --output "$REPORT_DIR/report.json"
trivy convert --format cyclonedx --output "$REPORT_DIR/sbom.cdx.json" "$REPORT_DIR/report.json"
# The vulnerability attestation carries fixable findings only: those are the
# ones a component owner can act on.
trivy image "$@" --ignore-unfixed --format cosign-vuln --output "$REPORT_DIR/vuln.cosign.json"

echo ">> All findings, including those without a fix (information only):"
trivy convert --format table --report summary "$REPORT_DIR/report.json"

echo ">> Fixable findings with severity $SEVERITY:"
set +e
trivy image "$@" --ignore-unfixed --severity "$SEVERITY" --exit-code 3 --format table
rc=$?
set -e

case "$rc" in
  0) RESULT=passed ;;
  3) RESULT=findings ;;
  *)
    echo ">> Error: Trivy failed with exit code $rc"
    exit "$rc"
    ;;
esac
printf '%s' "$RESULT" > /tmp/result.txt

cat > "$REPORT_DIR/provenance.json" <<EOF
{
  "buildDefinition": {
    "buildType": "https://openchoreo.dev/workflows/container-build/v1",
    "externalParameters": {
      "source": {
        "repository": "$(json_escape "$GIT_REPO")",
        "ref": "refs/heads/$(json_escape "$GIT_BRANCH")"
      }
    },
    "internalParameters": {
      "workflow": {
        "name": "$(json_escape "$WORKFLOW_NAME")",
        "namespace": "$(json_escape "$WORKFLOW_NAMESPACE")"
      }
    },
    "resolvedDependencies": [
      {
        "uri": "git+$(json_escape "$GIT_REPO")@refs/heads/$(json_escape "$GIT_BRANCH")",
        "digest": { "gitCommit": "$(json_escape "$GIT_COMMIT")" }
      }
    ]
  },
  "runDetails": {
    "builder": { "id": "$(json_escape "$BUILDER_ID")" },
    "metadata": {
      "invocationId": "$(json_escape "$WORKFLOW_UID")",
      "startedOn": "$STARTED_ON",
      "finishedOn": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    }
  }
}
EOF

if [ "$RESULT" = findings ]; then
  if [ "$MODE" = enforce ]; then
    echo ">> Error: Fixable vulnerabilities with severity $SEVERITY found."
    echo ">> Hint: Update the base image or the affected dependencies, or document"
    echo ">>       an exception with a reason and expiry in $IGNORE_FILE.yaml."
    exit 1
  fi
  echo ">> Warning: Fixable vulnerabilities with severity $SEVERITY found (audit mode, not failing)."
else
  echo ">> No fixable vulnerabilities with severity $SEVERITY."
fi
