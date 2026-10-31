// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package perfmetrics

// OpenChoreo pod labels. Container Insights records the full pod-label set
// inside KubePodInventory.PodLabel (a JSON string column); the KQL builder
// filters on these keys.
const (
	LabelComponentUID   = "openchoreo.dev/component-uid"
	LabelProjectUID     = "openchoreo.dev/project-uid"
	LabelEnvironmentUID = "openchoreo.dev/environment-uid"
	LabelReleaseLineUID = "openchoreo.dev/release-line-uid"
	// LabelReleaseLineComponentUID is the component UID on a release line's pods, which don't carry LabelComponentUID.
	LabelReleaseLineComponentUID = "openchoreo.dev/release-line-component-uid"
	LabelNamespace               = "openchoreo.dev/namespace"
)

// Container Insights table names.
const (
	PerfTable             = "Perf"
	KubePodInventoryTable = "KubePodInventory"
)

// Perf.CounterName values emitted by the Azure Monitor Agent for the
// K8SContainer object.
const (
	CounterCPUUsageNanoCores     = "cpuUsageNanoCores"
	CounterCPURequestNanoCores   = "cpuRequestNanoCores"
	CounterCPULimitNanoCores     = "cpuLimitNanoCores"
	CounterMemoryWorkingSetBytes = "memoryWorkingSetBytes"
	CounterMemoryRequestBytes    = "memoryRequestBytes"
	CounterMemoryLimitBytes      = "memoryLimitBytes"
)

// nanoCoresPerCore converts AMA's nanocore CPU counters to whole cores so the
// adapter reports CPU in the same unit as the AWS/Prometheus siblings.
const nanoCoresPerCore = 1e9
