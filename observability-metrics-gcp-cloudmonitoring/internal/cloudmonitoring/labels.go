// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudmonitoring

// OpenChoreo pod-label keys as they appear in Cloud Monitoring's
// metadata.user_labels for k8s_container time series.
//
// Unlike Cloud Logging (where the GKE agent surfaces pod labels under a
// "k8s-pod/" prefix with dots replaced by underscores), Monitoring system
// metadata keeps the raw label keys verbatim, dots included.
// Metric scoping is by UID only (see BuildResourceMetricsFilter); the
// namespace label is intentionally not used as a metric filter because the
// rule's namespace (data-plane) does not match the pod's control-plane
// openchoreo.dev/namespace label.
const (
	labelComponentUID   = "openchoreo.dev/component-uid"
	labelProjectUID     = "openchoreo.dev/project-uid"
	labelEnvironmentUID = "openchoreo.dev/environment-uid"
)

// zeroUUID is treated as "not set" by upstream callers.
const zeroUUID = "00000000-0000-0000-0000-000000000000"

// normalizeUID maps the zero UUID to empty so it never becomes a filter clause.
func normalizeUID(uid string) string {
	if uid == zeroUUID {
		return ""
	}
	return uid
}
