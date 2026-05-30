// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package loganalytics

// OpenChoreo pod labels propagated by the workload's pod template.
// AMA writes these into ContainerLogV2.PodLabels (a dynamic JSON column).
const (
	LabelComponentUID   = "openchoreo.dev/component-uid"
	LabelProjectUID     = "openchoreo.dev/project-uid"
	LabelEnvironmentUID = "openchoreo.dev/environment-uid"
	LabelNamespace      = "openchoreo.dev/namespace"

	LabelComponentName   = "openchoreo.dev/component"
	LabelProjectName     = "openchoreo.dev/project"
	LabelEnvironmentName = "openchoreo.dev/environment"
)

// WorkflowNamespacePrefix matches the synthesized namespace pattern that
// OpenChoreo's workflow plane uses (workflows-<openchoreoNamespace>).
const WorkflowNamespacePrefix = "workflows-"

// ContainerLogV2Table is the default Container Insights table for pod logs
// once the v2 schema is enabled via the container-azm-ms-agentconfig ConfigMap.
const ContainerLogV2Table = "ContainerLogV2"
