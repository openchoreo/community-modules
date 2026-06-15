// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package appinsights

// OpenChoreo pod labels propagated onto spans as resource attributes by the
// collector's k8s attributes processor. The azuremonitor exporter copies all
// resource attributes into the Properties column of AppRequests and
// AppDependencies, so these are the keys the KQL filters address.
const (
	LabelNamespace      = "openchoreo.dev/namespace"
	LabelComponentUID   = "openchoreo.dev/component-uid"
	LabelProjectUID     = "openchoreo.dev/project-uid"
	LabelEnvironmentUID = "openchoreo.dev/environment-uid"
)

// resourceAttributePrefixes identifies Properties keys that originate from
// OTel resource attributes rather than span attributes. The exporter merges
// both into the same Properties bag, so the split is reconstructed by key
// prefix when building API responses.
var resourceAttributePrefixes = []string{
	"openchoreo.dev/",
	"k8s.",
	"service.",
	"host.",
	"cloud.",
	"container.",
	"telemetry.",
}
