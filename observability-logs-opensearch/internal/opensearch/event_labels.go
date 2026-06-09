// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opensearch

// OpenSearch field paths for querying Kubernetes events.
//
// Unlike container logs (which are collected by Fluent-Bit and have their label
// dots replaced with underscores), Kubernetes events are written by the OpenTelemetry
// collector's opensearchexporter, which preserves dots in label keys. Therefore these
// constants must NOT use ReplaceDots and reference the dotted field paths directly.
const (
	// EvTimestamp is the event timestamp field.
	EvTimestamp = "@timestamp"
	// EvMessage is the event message field (stored as the OTEL log record body).
	EvMessage = "body"
	// EvSeverityText is the event type (e.g. Normal, Warning), stored as the OTEL severity text.
	EvSeverityText = "severity.text"
	// EvReason is the short, machine-readable reason for the event.
	EvReason = "attributes.k8s.event.reason"
	// EvObjectNamespace is the Kubernetes namespace the event was emitted in.
	EvObjectNamespace = "attributes.k8s.namespace.name"

	// EvObjectKind is the kind of the Kubernetes object the event involves.
	EvObjectKind = "resource.k8s.object.kind"
	// EvObjectName is the name of the Kubernetes object the event involves.
	EvObjectName = "resource.k8s.object.name"

	// evLabelPrefix is the prefix for OpenChoreo labels copied onto the event's
	// involved object by the k8seventenrich processor.
	evLabelPrefix = "resource.k8s.object.label."

	// EvComponentName is the OpenChoreo component name label field path.
	EvComponentName = evLabelPrefix + ComponentName
	// EvComponentID is the OpenChoreo component UID label field path.
	EvComponentID = evLabelPrefix + ComponentID
	// EvProjectName is the OpenChoreo project name label field path.
	EvProjectName = evLabelPrefix + ProjectName
	// EvProjectID is the OpenChoreo project UID label field path.
	EvProjectID = evLabelPrefix + ProjectID
	// EvEnvironmentName is the OpenChoreo environment name label field path.
	EvEnvironmentName = evLabelPrefix + EnvironmentName
	// EvEnvironmentID is the OpenChoreo environment UID label field path.
	EvEnvironmentID = evLabelPrefix + EnvironmentID
	// EvNamespaceName is the OpenChoreo namespace name label field path.
	EvNamespaceName = evLabelPrefix + NamespaceName
)
