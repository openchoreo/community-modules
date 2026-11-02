// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package loganalytics

const (
	LabelComponentUID   = "openchoreo.dev/component-uid"
	LabelProjectUID     = "openchoreo.dev/project-uid"
	LabelEnvironmentUID = "openchoreo.dev/environment-uid"
	LabelReleaseLineUID = "openchoreo.dev/release-line-uid"
	// LabelReleaseLineComponentUID is the component UID on a release line's pods, which don't carry LabelComponentUID.
	LabelReleaseLineComponentUID = "openchoreo.dev/release-line-component-uid"
	LabelNamespace               = "openchoreo.dev/namespace"

	LabelComponentName   = "openchoreo.dev/component"
	LabelProjectName     = "openchoreo.dev/project"
	LabelEnvironmentName = "openchoreo.dev/environment"

	WorkflowNamespacePrefix = "workflows-"
	ContainerLogV2Table     = "ContainerLogV2"
)
