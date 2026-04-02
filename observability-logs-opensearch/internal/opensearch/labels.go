// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opensearch

import "strings"

// ReplaceDots replaces dots with underscores in a string.
// This is used to match Fluent-Bit's Replace_Dots behavior in the OpenSearch output plugin.
func ReplaceDots(s string) string {
	return strings.ReplaceAll(s, ".", "_")
}

// Kubernetes label keys used for log filtering and identification.
const (
	ComponentID     = "openchoreo.dev/component-uid"
	EnvironmentID   = "openchoreo.dev/environment-uid"
	ProjectID       = "openchoreo.dev/project-uid"
	ComponentName   = "openchoreo.dev/component"
	EnvironmentName = "openchoreo.dev/environment"
	ProjectName     = "openchoreo.dev/project"
	Version         = "version"
	VersionID       = "version_id"
	NamespaceName   = "openchoreo.dev/namespace"
	BuildID         = "build-name"
	BuildUUID       = "uuid"
	Target          = "target"
)

// Target value constants for different log types.
const (
	TargetBuild   = "build"
	TargetRuntime = "runtime"
)

// Query parameter constants for log types.
const (
	QueryParamLogTypeBuild   = "BUILD"
	QueryParamLogTypeRuntime = "RUNTIME"
)

// OpenSearch field paths for querying Kubernetes labels in log documents.
const (
	KubernetesPrefix        = "kubernetes"
	KubernetesLabelsPrefix  = KubernetesPrefix + ".labels"
	KubernetesPodName       = KubernetesPrefix + ".pod_name"
	KubernetesContainerName = KubernetesPrefix + ".container_name"
	KubernetesNamespaceName = KubernetesPrefix + ".namespace_name"
)

// OpenSearch field paths with dots replaced by underscores in label keys.
var (
	OSComponentID   = KubernetesLabelsPrefix + "." + ReplaceDots(ComponentID)
	OSEnvironmentID = KubernetesLabelsPrefix + "." + ReplaceDots(EnvironmentID)
	OSProjectID     = KubernetesLabelsPrefix + "." + ReplaceDots(ProjectID)
	OSVersion       = KubernetesLabelsPrefix + "." + ReplaceDots(Version)
	OSVersionID     = KubernetesLabelsPrefix + "." + ReplaceDots(VersionID)
	OSNamespaceName = KubernetesLabelsPrefix + "." + ReplaceDots(NamespaceName)
	OSBuildID       = KubernetesLabelsPrefix + "." + ReplaceDots(BuildID)
	OSBuildUUID     = KubernetesLabelsPrefix + "." + ReplaceDots(BuildUUID)
	OSTarget        = KubernetesLabelsPrefix + "." + ReplaceDots(Target)
)
