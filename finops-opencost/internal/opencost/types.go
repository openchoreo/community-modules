// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opencost

import "time"

// OpenCost sanitizes Kubernetes label keys by replacing dots and dashes with
// underscores, so the openchoreo.dev/* labels are stored under these keys.
const (
	LabelNamespace      = "openchoreo_dev_namespace"
	LabelProject        = "openchoreo_dev_project"
	LabelProjectUID     = "openchoreo_dev_project_uid"
	LabelComponent      = "openchoreo_dev_component"
	LabelComponentUID   = "openchoreo_dev_component_uid"
	LabelEnvironment    = "openchoreo_dev_environment"
	LabelEnvironmentUID = "openchoreo_dev_environment_uid"
)

type AllocationResponse struct {
	Code int                     `json:"code"`
	Data []map[string]Allocation `json:"data"`
}

type Allocation struct {
	Name            string               `json:"name"`
	Properties      AllocationProperties `json:"properties"`
	Window          Window               `json:"window"`
	Start           time.Time            `json:"start"`
	End             time.Time            `json:"end"`
	CPUCost         float64              `json:"cpuCost"`
	RAMCost         float64              `json:"ramCost"`
	CPUEfficiency   float64              `json:"cpuEfficiency"`
	RAMEfficiency   float64              `json:"ramEfficiency"`
	TotalEfficiency float64              `json:"totalEfficiency"`
	CPUCoreHours    float64              `json:"cpuCoreHours"`
	RAMByteHours    float64              `json:"ramByteHours"`
}

type AllocationProperties struct {
	Namespace string            `json:"namespace"`
	Labels    map[string]string `json:"labels"`
}

type Window struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

func (a Allocation) Label(key string) string {
	return a.Properties.Labels[key]
}
