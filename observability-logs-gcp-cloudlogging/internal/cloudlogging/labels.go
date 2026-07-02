// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudlogging

import "strings"

const (
	// OpenChoreo pod-label keys, stamped onto workload pods by the
	// OpenChoreo controllers. These are the raw Kubernetes label keys.
	LabelComponentUID   = "openchoreo.dev/component-uid"
	LabelProjectUID     = "openchoreo.dev/project-uid"
	LabelEnvironmentUID = "openchoreo.dev/environment-uid"
	LabelNamespace      = "openchoreo.dev/namespace"

	LabelComponentName   = "openchoreo.dev/component"
	LabelProjectName     = "openchoreo.dev/project"
	LabelEnvironmentName = "openchoreo.dev/environment"

	// podLabelPrefix is how GKE's logging agent surfaces Kubernetes pod labels
	// on a LogEntry, under "k8s-pod/<key>".
	//
	// IMPORTANT: the modern GKE managed (Fluent Bit) agent replaces DOTS in the
	// key with underscores (slashes/hyphens preserved), e.g. the pod label
	// openchoreo.dev/component-uid surfaces as
	// labels."k8s-pod/openchoreo_dev/component-uid" — a filter using the raw
	// dotted key matches nothing. This is undocumented and differs from the
	// legacy fluentd agent, so it is toggleable via SanitizePodLabelDots.
	podLabelPrefix = "k8s-pod/"

	// k8sContainerResource is the GKE monitored-resource type for
	// application container logs.
	k8sContainerResource = "k8s_container"

	// WorkflowNamespacePrefix is Argo's namespace convention for workflow pods.
	WorkflowNamespacePrefix = "workflows-"
)

// SanitizePodLabelDots toggles the dot->underscore substitution in podLabelKey
// (see podLabelPrefix); set from config at startup, defaults true.
var SanitizePodLabelDots = true

// podLabelKey returns the LogEntry label-map key for a raw Kubernetes pod
// label key, applying the dot->underscore substitution when enabled.
func podLabelKey(rawKey string) string {
	if SanitizePodLabelDots {
		rawKey = strings.ReplaceAll(rawKey, ".", "_")
	}
	return podLabelPrefix + rawKey
}
