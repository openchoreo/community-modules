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

	// podLabelPrefix is how GKE's logging agent surfaces Kubernetes pod
	// labels on a LogEntry: it maps each pod label under "k8s-pod/<key>".
	//
	// IMPORTANT: the modern GKE managed (Fluent Bit) logging agent sanitizes
	// the label key by replacing DOTS with underscores; slashes and hyphens
	// are preserved. Verified against live cluster entries, e.g. the pod label
	//   openchoreo.dev/component-uid
	// is surfaced on the LogEntry as
	//   labels."k8s-pod/openchoreo_dev/component-uid"
	// and topology.kubernetes.io/region as
	//   labels."k8s-pod/topology_kubernetes_io/region"
	// Building the filter with the raw dotted key matches nothing.
	//
	// This transform is NOT documented by Google (the managed agent config is
	// closed-source) and differs from the legacy fluentd agent, which did no
	// substitution. It is therefore treated as observed-default behaviour and
	// can be turned off per-cluster via SanitizePodLabelDots — see config.
	podLabelPrefix = "k8s-pod/"

	// k8sContainerResource is the GKE monitored-resource type for
	// application container logs.
	k8sContainerResource = "k8s_container"

	// WorkflowNamespacePrefix is Argo's namespace convention for workflow pods.
	WorkflowNamespacePrefix = "workflows-"
)

// SanitizePodLabelDots controls whether podLabelKey replaces dots with
// underscores in the label key, matching the modern GKE managed logging
// agent. It defaults to true (the observed behaviour on current GKE) and is
// set from config at startup so a cluster running a different agent — one
// that preserves dots — can disable it without a rebuild.
var SanitizePodLabelDots = true

// podLabelKey returns the LogEntry label-map key for a raw Kubernetes pod
// label key. With sanitization on (the default), dots are replaced with
// underscores, e.g. "openchoreo.dev/component-uid" ->
// "k8s-pod/openchoreo_dev/component-uid". With it off, the key is used
// verbatim, e.g. "k8s-pod/openchoreo.dev/component-uid".
func podLabelKey(rawKey string) string {
	if SanitizePodLabelDots {
		rawKey = strings.ReplaceAll(rawKey, ".", "_")
	}
	return podLabelPrefix + rawKey
}
