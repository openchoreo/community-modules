// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opensearch

import (
	"encoding/json"
	"fmt"
	"time"
)

// AuditRecord is one stored audit event. The json tags are the record's own snake_case
// spelling, so a stored document unmarshals into this directly.
type AuditRecord struct {
	SchemaVersion string         `json:"schema_version"`
	EventID       string         `json:"event_id"`
	EventTime     time.Time      `json:"event_time"`
	Actor         AuditActor     `json:"actor"`
	Action        string         `json:"action"`
	Category      string         `json:"category"`
	Result        string         `json:"result"`
	RequestID     string         `json:"request_id"`
	SourceIP      string         `json:"source_ip"`
	UserAgent     string         `json:"user_agent"`
	Producer      string         `json:"producer"`
	Surface       string         `json:"surface"`
	OperationID   string         `json:"operation_id"`
	HTTP          *AuditHTTPInfo `json:"http"`
	Resource      *AuditResource `json:"resource"`
	Request       *AuditRequest  `json:"request"`
	Returned      *AuditReturned `json:"returned"`
	Metadata      map[string]any `json:"metadata"`

	// Stamped by the collector rather than the emitting service, so they are worth
	// comparing against the record's own producer claim.
	Kubernetes      AuditCollectorInfo `json:"kubernetes"`
	ClusterInstance string             `json:"openchoreo_cluster_instance"`
}

// AuditActor is who performed the action. ID is unique only within Issuer.
type AuditActor struct {
	Type         string              `json:"type"`
	ID           string              `json:"id"`
	Issuer       string              `json:"issuer"`
	SessionID    string              `json:"session_id"`
	Entitlements map[string][]string `json:"entitlements"`
}

// AuditHTTPInfo is the request line, for an event that arrived over HTTP.
type AuditHTTPInfo struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// AuditResource is the target resource and the point in the tree the decision was
// authorized at.
type AuditResource struct {
	Type      string `json:"type"`
	Namespace string `json:"namespace"`

	// Environment is dual-scoped as {namespace}/{name}, stored exactly as
	// authorization evaluated it, unlike its bare-name siblings above and below.
	Environment string         `json:"environment"`
	Project     string         `json:"project"`
	Component   string         `json:"component"`
	ReleaseLine string         `json:"release_line"`
	Resource    string         `json:"resource"`
	UID         string         `json:"uid"`
	Name        string         `json:"name"`
	Metadata    map[string]any `json:"metadata"`
}

// AuditRequest is what the operation was asked for. The index template leaves it
// unmapped, so it is kept in _source but cannot be searched.
type AuditRequest struct {
	Parameters map[string]any `json:"parameters"`
}

// AuditReturned is what the operation handed back. Count can exceed len(Items) when the
// producer cut the list.
type AuditReturned struct {
	Count int                 `json:"count"`
	Items []AuditReturnedItem `json:"items"`
}

// AuditReturnedItem is one object the operation returned.
type AuditReturnedItem struct {
	Type      string `json:"type"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	UID       string `json:"uid"`
}

// AuditCollectorInfo is where the collector read the record from.
type AuditCollectorInfo struct {
	NamespaceName string `json:"namespace_name"`
	PodName       string `json:"pod_name"`
	ContainerName string `json:"container_name"`
}

// AuditFilterValue is one value a filter takes, with how many records carry it.
type AuditFilterValue struct {
	Value string
	Count int64
}

// AuditTimeline is per-interval counts across the queried window.
type AuditTimeline struct {
	Interval string
	Buckets  []AuditTimelineBucket
}

// AuditTimelineBucket is one interval of the timeline.
type AuditTimelineBucket struct {
	StartTime time.Time
	Total     int64
	Counts    map[string]int64
}

// ParseAuditRecord reads a search hit into an audit record, erroring rather than
// returning a zero-valued one that would read as a real finding.
func ParseAuditRecord(hit Hit) (AuditRecord, error) {
	raw, err := json.Marshal(hit.Source)
	if err != nil {
		return AuditRecord{}, fmt.Errorf("failed to re-encode document: %w", err)
	}

	var record AuditRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return AuditRecord{}, fmt.Errorf("failed to parse document: %w", err)
	}

	// action and category are deliberately absent: a record with no resolved operation
	// carries them empty, and such records are worth keeping.
	for _, required := range []struct{ field, value string }{
		{"schema_version", record.SchemaVersion},
		{"event_id", record.EventID},
		{"actor.type", record.Actor.Type},
		{"actor.id", record.Actor.ID},
		{"result", record.Result},
	} {
		if required.value == "" {
			return AuditRecord{}, fmt.Errorf("document has no %s", required.field)
		}
	}
	if record.EventTime.IsZero() {
		return AuditRecord{}, fmt.Errorf("document has no event_time")
	}

	return record, nil
}
