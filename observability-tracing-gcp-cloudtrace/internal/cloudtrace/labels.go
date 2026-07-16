// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudtrace

// OpenChoreo pod labels propagated onto spans by the collector pipeline: the
// k8s attributes processor copies them onto the resource, and the
// transform/openchoreo_annotations processor promotes them to span
// attributes because the googlecloud exporter does not write resource
// attributes onto Cloud Trace spans. Cloud Trace surfaces span attributes as
// v1 labels with the keys verbatim, so these are the keys the ListTraces
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
// resource attributes (or Cloud Trace agent metadata) rather than span
// attributes. Cloud Trace flattens everything into one labels map, so the
// split is reconstructed by key prefix when building API responses.
// Unmatched keys are treated as span attributes.
var resourceAttributePrefixes = []string{
	"openchoreo.dev/",
	"k8s.",
	"service.",
	"host.",
	"cloud.",
	"container.",
	"telemetry.",
	"g.co/",
}
