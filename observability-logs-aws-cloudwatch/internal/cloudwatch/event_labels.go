// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatch

import "fmt"

// CloudWatch Logs Insights field paths for querying Kubernetes events.
//
// Events are NOT collected by Fluent Bit (which produces the `kubernetes.labels.*`
// shape the container-log query builder targets). They are collected by the
// observability-events-otel-collector distribution and shipped by the OTEL
// `awscloudwatchlogsexporter`, which serialises each log record to a JSON envelope
// of the form:
//
//	{
//	  "body": "<event message>",
//	  "severity_text": "Normal" | "Warning",
//	  "attributes": { "k8s.event.reason": "...", "k8s.namespace.name": "...", ... },
//	  "resource":   { "k8s.object.kind": "...", "k8s.object.name": "...",
//	                  "k8s.object.label.openchoreo.dev/component-uid": "...", ... }
//	}
//
// CloudWatch Logs Insights auto-discovers JSON fields and flattens nested objects
// with dot notation, so the enriched OpenChoreo labels land at
// `resource.k8s.object.label.<key>`. This is the CloudWatch analog of the
// OpenSearch adapter's event_labels.go.
//
// NOTE: These paths mirror the awscloudwatchlogsexporter's documented serialisation
// (severity_text/attributes/resource) and must be confirmed against real records in
// the events log group. The container-log `labelField` / `label*` constants MUST NOT
// be reused here — they target the Fluent Bit shape.
const (
	// evTimestamp is the built-in Insights ingestion timestamp column.
	evTimestamp = "@timestamp"
	// evMessage is the event message, stored as the OTEL log record body.
	evMessage = "body"
	// evSeverityText is the event type (Normal/Warning), stored as OTEL severity text.
	evSeverityText = "severity_text"
	// evReason is the short, machine-readable reason for the event.
	evReason = "attributes.k8s.event.reason"
	// evObjectNamespace is the Kubernetes namespace the event was emitted in.
	evObjectNamespace = "attributes.k8s.namespace.name"
	// evObjectKind is the kind of the Kubernetes object the event involves.
	evObjectKind = "resource.k8s.object.kind"
	// evObjectName is the name of the Kubernetes object the event involves.
	evObjectName = "resource.k8s.object.name"

	// evLabelPrefix is the flattened path prefix for OpenChoreo labels copied onto
	// the event's involved object by the k8seventenrich processor.
	evLabelPrefix = "resource.k8s.object.label."
)

// Event label field paths, derived from the same OpenChoreo label keys the
// container-log query builder uses (labelComponentUID, etc.).
var (
	evComponentUID    = evLabelPrefix + labelComponentUID
	evComponentName   = evLabelPrefix + labelComponentName
	evEnvironmentUID  = evLabelPrefix + labelEnvironmentUID
	evEnvironmentName = evLabelPrefix + labelEnvironmentName
	evProjectUID      = evLabelPrefix + labelProjectUID
	evProjectName     = evLabelPrefix + labelProjectName
	evNamespaceName   = evLabelPrefix + labelNamespace
)

// eventField renders a CloudWatch Insights accessor for an event field path.
// Insights flattens nested JSON into dotted field names; those paths contain dots
// (and, for labels, slashes) so the whole path must be wrapped in backticks.
func eventField(path string) string {
	return fmt.Sprintf("`%s`", path)
}
