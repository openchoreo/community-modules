// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudtrace

// OpenChoreo pod labels promoted to span attributes by the collector's
// transform processor (the googlecloud exporter does not write resource
// attributes onto Cloud Trace spans). Cloud Trace surfaces them as v1 span
// labels with the keys verbatim, so these are the keys the ListTraces
// filters address.
const (
	LabelNamespace      = "openchoreo.dev/namespace"
	LabelComponentUID   = "openchoreo.dev/component-uid"
	LabelProjectUID     = "openchoreo.dev/project-uid"
	LabelEnvironmentUID = "openchoreo.dev/environment-uid"
)

// zeroUUID is treated as "not set" by upstream callers.
const zeroUUID = "00000000-0000-0000-0000-000000000000"

// normalizeUID maps the zero UUID to empty so it never becomes a filter term.
func normalizeUID(uid string) string {
	if uid == zeroUUID {
		return ""
	}
	return uid
}

// resourceAttributePrefixes identifies label keys that originate from OTel
// resource attributes rather than span attributes. Cloud Trace flattens both
// into one labels map, so the split is reconstructed by key prefix when
// building API responses.
// The g.co/ prefix is deliberately excluded: Cloud Trace uses it for span
// metadata (g.co/status/*, g.co/agent) as well as resource metadata, so it
// cannot cleanly classify as either. Such keys fall through to span
// attributes.
var resourceAttributePrefixes = []string{
	"openchoreo.dev/",
	"k8s.",
	"service.",
	"host.",
	"cloud.",
	"container.",
	"telemetry.",
}
