// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package openobserve

import (
	"regexp"
	"strings"
)

// labelKeyPattern is the Kubernetes label key grammar: an optional DNS-subdomain prefix
// followed by "/", then a name of alphanumerics with "-", "_" and "." allowed inside.
//
// Label keys become column names rather than string literals, so they cannot be escaped
// the way a value can. Anything outside this grammar is rejected instead: a key like
// `x = 1 OR 1=1 OR y` would otherwise be concatenated into the predicate verbatim and make
// it always true, which on this endpoint means returning records the caller never selected.
var labelKeyPattern = regexp.MustCompile(
	`^([a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*/)?` +
		`[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$`)

// IsValidLabelKey reports whether a key is a well-formed Kubernetes label key.
func IsValidLabelKey(key string) bool {
	// 253 for the prefix, "/", and 63 for the name - a cheap bail before the pattern.
	if key == "" || len(key) > 317 {
		return false
	}
	if !labelKeyPattern.MatchString(key) {
		return false
	}

	// Kubernetes bounds the two halves separately, and the pattern says nothing about
	// length. Checking only the whole key would admit a name longer than any pod could
	// carry, and a filter on it would query a column that cannot exist - matching nothing,
	// with no indication the key was the problem.
	name := key
	if slash := strings.IndexByte(key, '/'); slash >= 0 {
		if slash > 253 {
			return false
		}
		name = key[slash+1:]
	}
	return len(name) <= 63
}

// OpenObserve column names for querying container logs by raw Kubernetes coordinates.
//
// OpenObserve is schema-on-write: Fluent Bit posts the record as JSON and OpenObserve
// flattens it into columns, replacing ".", "/" and "-" in keys with "_". These constants
// are that flattening applied to the Kubernetes metadata Fluent Bit attaches.
const (
	// ClusterInstanceField is the record-level cluster identifier the logs collector
	// stamps. The key is flat - no dots, no slashes, no hyphens - so it survives
	// OpenObserve's column-name normalisation unchanged.
	ClusterInstanceField = "openchoreo_cluster_instance"

	// colTimestamp is the record timestamp, microseconds since epoch.
	colTimestamp = "_timestamp"
	// colLog is the log message.
	colLog = "log"

	colNamespaceName  = "kubernetes_namespace_name"
	colPodName        = "kubernetes_pod_name"
	colContainerName  = "kubernetes_container_name"
	colPodIP          = "kubernetes_pod_ip"
	colHost           = "kubernetes_host"
	colContainerImage = "kubernetes_container_image"

	// labelColumnPrefix is the column prefix for flattened pod labels.
	labelColumnPrefix = "kubernetes_labels_"
)

// mangleLabelKey turns a Kubernetes label key into the column OpenObserve stores it in.
//
// OpenObserve collapses ".", "/" and "-" to "_" when it flattens a record, so
// openchoreo.dev/component-uid is stored as kubernetes_labels_openchoreo_dev_component_uid.
// Callers pass the key as Kubernetes spells it and this is the only place that has to know.
//
// The transform is lossy and collisions are possible: "a.b/c", "a/b-c" and "a_b_c" all
// become "a_b_c", so a filter on one matches records carrying any of them. Nothing here can
// undo that - the collision is created at ingest, and this only mirrors the transform so the
// column named is the one OpenObserve wrote.
func mangleLabelKey(key string) string {
	return strings.NewReplacer(".", "_", "/", "_", "-", "_").Replace(key)
}

// labelColumnName returns the column a pod label is stored under, as OpenObserve names it.
func labelColumnName(key string) string {
	return labelColumnPrefix + mangleLabelKey(key)
}

// labelColumn returns that column quoted, ready to sit in SQL.
//
// Quoting is belt and braces: callers reject keys outside the Kubernetes grammar before
// reaching here, and a quoted identifier cannot break out of its own name even if one
// slipped through.
func labelColumn(key string) string {
	return quoteIdentifier(labelColumnName(key))
}

// knownLabelKeys are the label keys this adapter can spell back in Kubernetes form.
//
// mangleLabelKey is not injective, so a stored column cannot be turned back into a label
// key by manipulating the string - "openchoreo_dev_component_uid" could have come from
// "openchoreo.dev/component-uid", "openchoreo/dev/component/uid", or a label literally
// named "openchoreo_dev_component_uid". The only sound reverse is a lookup, so this lists
// the keys OpenChoreo and the common ecosystem charts actually set. Anything outside it is
// returned in its stored form rather than guessed at.
var knownLabelKeys = []string{
	"openchoreo.dev/component",
	"openchoreo.dev/component-uid",
	"openchoreo.dev/environment",
	"openchoreo.dev/environment-uid",
	"openchoreo.dev/project",
	"openchoreo.dev/project-uid",
	"openchoreo.dev/release-line",
	"openchoreo.dev/release-line-uid",
	"openchoreo.dev/release-line-component-uid",
	"openchoreo.dev/namespace",
	"openchoreo.dev/plane",
	"openchoreo.dev/plane-id",
	"openchoreo.dev/system-component",
	"app.kubernetes.io/name",
	"app.kubernetes.io/instance",
	"app.kubernetes.io/component",
	"app.kubernetes.io/managed-by",
	"app.kubernetes.io/part-of",
	"app.kubernetes.io/version",
	"apps.kubernetes.io/pod-index",
	"statefulset.kubernetes.io/pod-name",
	"workflows.argoproj.io/workflow",
	"batch.kubernetes.io/job-name",
	"batch.kubernetes.io/controller-uid",
	"gateway.networking.k8s.io/gateway-name",
	"gateway.networking.k8s.io/gateway-class-name",
	"helm.sh/chart",
	"pod-template-hash",
	"controller-revision-hash",
	"controller-uid",
	"pod-template-generation",
	"build-name",
	"version",
	"version_id",
	"target",
	"uuid",
}

// labelKeysByColumn maps a stored column name back to the Kubernetes label key it came
// from. Built once from knownLabelKeys.
var labelKeysByColumn = func() map[string]string {
	m := make(map[string]string, len(knownLabelKeys))
	for _, key := range knownLabelKeys {
		m[mangleLabelKey(key)] = key
	}
	return m
}()

// restoreLabelKey turns a stored label column back into the Kubernetes label key, as far as
// that is possible.
//
// A key this adapter knows is returned exactly as Kubernetes spells it, so it can be sent
// straight back as a filter. One it does not know is returned with only the column prefix
// stripped - still useful to read, but it will not round-trip as a filter, because the
// separators it was spelled with are not recoverable.
func restoreLabelKey(column string) string {
	suffix := strings.TrimPrefix(column, labelColumnPrefix)
	if key, ok := labelKeysByColumn[suffix]; ok {
		return key
	}
	return suffix
}
