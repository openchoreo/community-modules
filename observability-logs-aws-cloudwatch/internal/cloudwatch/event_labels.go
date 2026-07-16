// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatch

import "fmt"

// CloudWatch Logs Insights field paths for events shipped by the OTEL
// awscloudwatchlogsexporter. Insights flattens nested JSON with dot notation.
const (
	evTimestamp       = "@timestamp"
	evMessage         = "body"
	evSeverityText    = "severity_text" // event type (Normal/Warning)
	evReason          = "attributes.k8s.event.reason"
	evObjectNamespace = "attributes.k8s.namespace.name"
	evObjectKind      = "resource.k8s.object.kind"
	evObjectName      = "resource.k8s.object.name"
	evLabelPrefix     = "resource.k8s.object.label."
)

var (
	evComponentUID    = evLabelPrefix + labelComponentUID
	evComponentName   = evLabelPrefix + labelComponentName
	evEnvironmentUID  = evLabelPrefix + labelEnvironmentUID
	evEnvironmentName = evLabelPrefix + labelEnvironmentName
	evProjectUID      = evLabelPrefix + labelProjectUID
	evProjectName     = evLabelPrefix + labelProjectName
	evNamespaceName   = evLabelPrefix + labelNamespace
)

// eventField wraps a dotted/slashed field path in backticks for Insights.
func eventField(path string) string {
	return fmt.Sprintf("`%s`", path)
}
