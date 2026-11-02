// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package loganalytics

import (
	"regexp"
	"strings"
)

// Kubernetes events are shipped by the observability-events-otel-collector
// module over Azure Monitor's native OTLP ingestion, which lands them in the
// built-in OTelLogs table. That table is shared by every OTLP log source routed
// to the workspace, so events are told apart by the instrumentation scope the
// k8s events receiver stamps on every record.
const (
	DefaultEventsTable     = "OTelLogs"
	DefaultEventsScopeName = "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/k8seventsreceiver"
)

// Keys the k8s events receiver and the k8seventenrich processor write. Log
// record attributes land in the Attributes column; resource attributes, which
// carry the involved object and its labels, land in ResourceAttributes.
const (
	attrEventReason     = "k8s.event.reason"
	attrObjectNamespace = "k8s.namespace.name"

	resObjectKind  = "k8s.object.kind"
	resObjectName  = "k8s.object.name"
	resLabelPrefix = "k8s.object.label."
)

// The helpers below are the one place that decides how an OTLP attribute is
// addressed inside OTelLogs' dynamic columns. The ingestion pipeline keeps
// attribute keys flat, so a dotted key is a single bag key rather than a path.

func logAttr(key string) string {
	return "tostring(Attributes[" + kqlString(key) + "])"
}

func resAttr(key string) string {
	return "tostring(ResourceAttributes[" + kqlString(key) + "])"
}

func eventLabel(label string) string {
	return resAttr(resLabelPrefix + label)
}

// eventComponentUID is the UID of the component an event's object belongs to. A
// release line's objects carry it under their own label.
var eventComponentUID = "coalesce(" + eventLabel(LabelComponentUID) + ", " + eventLabel(LabelReleaseLineComponentUID) + ")"

// hasSafeValue matches values whose JSON rendering is the value itself, which
// is what makes a `has` term pre-filter over the dynamic column a strict
// superset of the exact match that follows it. UIDs, Kubernetes names and
// event reasons all fit.
var hasSafeValue = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// dynEquals renders `expr == value` for an expr read out of the dynamic column
// col.
func dynEquals(col, expr, value string) string {
	return dynMatch(col, value, expr+" == "+kqlString(value))
}

// dynMatch renders pred, which holds only for rows whose dynamic column col
// carries value. When the value allows it, a `has` pre-filter over the whole
// column goes first: it can use the column's term index, where the extraction
// cannot.
func dynMatch(col, value, pred string) string {
	if !hasSafeValue.MatchString(value) {
		return pred
	}
	return col + " has " + kqlString(value) + "\n| where " + pred
}

// dynIn renders `expr in (values)`, with a `has_any` pre-filter when every
// value allows one.
func dynIn(col, expr string, values []string) string {
	var list strings.Builder
	writeKQLList(&list, values)

	in := expr + " in (" + list.String() + ")"
	for _, v := range values {
		if !hasSafeValue.MatchString(v) {
			return in
		}
	}
	return col + " has_any (" + list.String() + ")\n| where " + in
}
