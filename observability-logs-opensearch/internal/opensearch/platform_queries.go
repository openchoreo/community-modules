// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opensearch

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// ClusterInstanceField is the record-level cluster identifier the logs collector stamps.
// It is a flat key - no dots, no slashes - so it survives Fluent Bit's Replace_Dots and
// each backend's own field-name normalisation unchanged.
const ClusterInstanceField = "openchoreo_cluster_instance"

// PlatformLogsQueryParams holds parameters for a platform log query.
// Every filter is optional except the time range. Multi-value fields OR within
// themselves and AND with each other, and an empty field is not a filter.
type PlatformLogsQueryParams struct {
	ClusterInstances []string
	Namespaces       []string
	PodNames         []string
	ContainerNames   []string
	Labels           map[string]string
	StartTime        string
	EndTime          string
	SearchPhrase     string
	LogLevels        []string
	Limit            int
	SortOrder        string
}

// BuildPlatformLogsQuery builds a query over raw Kubernetes coordinates, with no
// project/component/environment correlation.
func (qb *QueryBuilder) BuildPlatformLogsQuery(params PlatformLogsQueryParams) map[string]interface{} {
	mustConditions := []map[string]interface{}{}
	mustConditions = addTimeRangeFilter(mustConditions, params.StartTime, params.EndTime)
	mustConditions = addTermsFilter(mustConditions, ClusterInstanceField, params.ClusterInstances)
	mustConditions = addTermsFilter(mustConditions, KubernetesNamespaceName, params.Namespaces)
	mustConditions = addTermsFilter(mustConditions, KubernetesPodName, params.PodNames)
	mustConditions = addTermsFilter(mustConditions, KubernetesContainerName, params.ContainerNames)
	mustConditions = addLabelFilters(mustConditions, params.Labels)
	mustConditions = addSearchPhraseFilter(mustConditions, params.SearchPhrase)
	mustConditions = addLogLevelFilter(mustConditions, params.LogLevels)

	limit := params.Limit
	if limit <= 0 {
		limit = 100
	}
	sortOrder := params.SortOrder
	if sortOrder == "" {
		sortOrder = "desc"
	}

	return map[string]interface{}{
		"size": limit,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": mustConditions,
			},
		},
		"sort": []map[string]interface{}{
			{
				"@timestamp": map[string]interface{}{
					"order": sortOrder,
				},
			},
		},
	}
}

// addTermsFilter ORs the given values on one field. An empty slice is not a filter.
func addTermsFilter(
	mustConditions []map[string]interface{}, field string, values []string,
) []map[string]interface{} {
	if len(values) == 0 {
		return mustConditions
	}
	return append(mustConditions, map[string]interface{}{
		"terms": map[string]interface{}{
			field: values,
		},
	})
}

// addLabelFilters ANDs one term per label pair.
//
// Label keys are rewritten with ReplaceDots to match how Fluent Bit stores them: the
// OpenSearch output runs with Replace_Dots On, so a pod labelled openchoreo.dev/plane is
// indexed at kubernetes.labels.openchoreo_dev/plane. Callers pass the label key as
// Kubernetes spells it and this is the only place that has to know.
//
// Keys are sorted so the query is deterministic, which keeps it comparable in tests and
// stable in logs.
//
// Replace_Dots is lossy in the name part of a key, so a filter can be ambiguous:
// "example.io/release.id" and "example.io/release_id" are both stored at
// kubernetes.labels.example_io/release_id, and a filter on either matches records carrying
// the other. Nothing here can undo that - the collision is created at ingest, and the
// adapter only mirrors the transform so the term targets the field Fluent Bit wrote.
// Resolving it means storing labels losslessly (key/value pairs rather than object
// fields), which is an ingest and index-mapping change, not an adapter one.
func addLabelFilters(
	mustConditions []map[string]interface{}, labels map[string]string,
) []map[string]interface{} {
	if len(labels) == 0 {
		return mustConditions
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		mustConditions = append(mustConditions, map[string]interface{}{
			"term": map[string]interface{}{
				KubernetesLabelsPrefix + "." + ReplaceDots(k): labels[k],
			},
		})
	}
	return mustConditions
}

// PlatformLogEntry is a parsed platform log entry: the message plus the physical
// coordinates and pod metadata of whatever produced it.
type PlatformLogEntry struct {
	Timestamp       time.Time         `json:"timestamp"`
	Log             string            `json:"log"`
	LogLevel        string            `json:"logLevel"`
	ClusterInstance string            `json:"clusterInstance"`
	NamespaceName   string            `json:"namespaceName"`
	PodName         string            `json:"podName"`
	ContainerName   string            `json:"containerName"`
	PodIP           string            `json:"podIp"`
	NodeName        string            `json:"nodeName"`
	ContainerImage  string            `json:"containerImage"`
	Labels          map[string]string `json:"labels"`
}

// ParsePlatformLogEntry converts a search hit to a PlatformLogEntry.
//
// It errors when the document cannot yield a valid entry. timestamp and log are
// required by the contract this module serves, and only here is the source visible:
// once parsed, an empty Log is ambiguous, since a genuinely blank line and a missing
// or non-string field both read as "". A blank line is real container output and is
// returned normally; an absent or non-string field is a malformed document.
func ParsePlatformLogEntry(hit Hit) (PlatformLogEntry, error) {
	source := hit.Source

	ts, ok := source["@timestamp"].(string)
	if !ok {
		return PlatformLogEntry{}, fmt.Errorf("@timestamp is absent or not a string")
	}
	timestamp, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return PlatformLogEntry{}, fmt.Errorf("@timestamp %q is not RFC3339: %w", ts, err)
	}

	log, ok := source["log"].(string)
	if !ok {
		return PlatformLogEntry{}, fmt.Errorf("log is absent or not a string")
	}

	entry := PlatformLogEntry{
		Timestamp:       timestamp,
		Log:             log,
		LogLevel:        extractLogLevel(log),
		ClusterInstance: getStringValue(source, ClusterInstanceField),
	}
	if k8s, ok := source["kubernetes"].(map[string]interface{}); ok {
		entry.NamespaceName = getStringValue(k8s, "namespace_name")
		entry.PodName = getStringValue(k8s, "pod_name")
		entry.ContainerName = getStringValue(k8s, "container_name")
		entry.PodIP = getStringValue(k8s, "pod_ip")
		// Fluent Bit's kubernetes filter calls the node "host".
		entry.NodeName = getStringValue(k8s, "host")
		entry.ContainerImage = getStringValue(k8s, "container_image")

		if labels, ok := k8s["labels"].(map[string]interface{}); ok {
			entry.Labels = make(map[string]string, len(labels))
			for k, v := range labels {
				if str, ok := v.(string); ok {
					entry.Labels[RestoreLabelKey(k)] = str
				}
			}
		}
	}
	return entry, nil
}

// RestoreLabelKey undoes Fluent Bit's Replace_Dots on a label key, so a caller sees the
// key as Kubernetes spells it and can paste it straight back into the label filter.
//
// Only the prefix - everything before the first "/" - is restored. A Kubernetes label
// prefix is a DNS subdomain and cannot contain an underscore, so every "_" there was a
// "."; the name part after the "/" may legitimately contain underscores (the workload
// labels "version_id" and "build-name" among them) and is left alone. Reversing the
// whole key would corrupt those.
//
// Dots in the name part are not recoverable either, and for the same reason: a stored
// "example_io/release_id" may have been "example.io/release.id" or "example.io/release_id",
// and the two are indistinguishable once written. Restoring the name part would corrupt the
// common case ("version_id", "build-name") to fix the rare one, so it is left as stored and
// such a key is reported with an underscore where a dot was.
//
// The same ambiguity makes a label filter on either spelling match records carrying the
// other. Both are consequences of Replace_Dots at ingest; the fix is to store labels
// losslessly rather than to post-process here.
func RestoreLabelKey(key string) string {
	slash := strings.Index(key, "/")
	if slash < 0 {
		return key
	}
	return strings.ReplaceAll(key[:slash], "_", ".") + key[slash:]
}
