// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package openobserve

import (
	"encoding/json"
	"fmt"
	"time"
)

// AuditLogsParams holds the filters for an audit log query.
type AuditLogsParams struct {
	StartTime time.Time
	EndTime   time.Time

	ActorIDs          []string
	ActorTypes        []string
	ActorIssuers      []string
	ActorSessionIDs   []string
	ActorEntitlements []string

	ResourceTypes        []string
	ResourceNamespaces   []string
	ResourceEnvironments []string
	ResourceProjects     []string
	ResourceComponents   []string
	ResourceResources    []string
	ResourceNames        []string

	ReturnedItemNames []string

	Actions      []string
	Categories   []string
	Results      []string
	Producers    []string
	Surfaces     []string
	OperationIDs []string
	RequestIDs   []string
	EventIDs     []string
	SourceIPs    []string
	UserAgents   []string

	SearchPhrase string
	Limit        int
	SortOrder    string
}

// AuditRecord is one audit event, decoded from the producer's log line.
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

	// Read from the collector's columns, never from the producer's line.
	Collector AuditCollectorInfo `json:"-"`
}

// AuditActor is who performed the action. ID is unique only within Issuer.
type AuditActor struct {
	Type         string              `json:"type"`
	ID           string              `json:"id"`
	Issuer       string              `json:"issuer"`
	SessionID    string              `json:"session_id"`
	Entitlements map[string][]string `json:"entitlements"`
}

// AuditHTTPInfo is the request line of an event that arrived over HTTP.
type AuditHTTPInfo struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// AuditResource is the target resource and where authorization was decided.
type AuditResource struct {
	Type        string         `json:"type"`
	Namespace   string         `json:"namespace"`
	Environment string         `json:"environment"`
	Project     string         `json:"project"`
	Component   string         `json:"component"`
	Resource    string         `json:"resource"`
	UID         string         `json:"uid"`
	Name        string         `json:"name"`
	Metadata    map[string]any `json:"metadata"`
}

// AuditRequest is what the operation was asked for. It is read from the log line: the
// flattened request_parameters_* columns lowercase keys and can't be unflattened.
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
	NamespaceName string
	PodName       string
	ContainerName string
}

// AuditLogsResult is a page of audit records with the total and optional timeline.
type AuditLogsResult struct {
	Records  []AuditRecord
	Total    int64
	Took     int
	Timeline *AuditTimeline
}

// AuditFilterValue is one value a filter takes, with how many records carry it.
type AuditFilterValue struct {
	Value string
	Count int64
}

// AuditFilterValues is the parsed result of an audit filter-values query.
type AuditFilterValues struct {
	Values      []AuditFilterValue
	TotalValues int64
	Took        int
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

// ParseAuditRecord decodes a row's log line rather than its flattened columns, which
// stringify metadata nested past OpenObserve's flatten level. A row missing identity
// fields is an error rather than a zero-valued record.
func ParseAuditRecord(row map[string]interface{}) (AuditRecord, error) {
	line, ok := row[colLog].(string)
	if !ok || line == "" {
		return AuditRecord{}, fmt.Errorf("row has no log line")
	}

	var record AuditRecord
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		return AuditRecord{}, fmt.Errorf("failed to parse log line: %w", err)
	}

	// action and category are not required: a record with no resolved operation carries them empty.
	for _, required := range []struct{ field, value string }{
		{"schema_version", record.SchemaVersion},
		{"event_id", record.EventID},
		{"actor.type", record.Actor.Type},
		{"actor.id", record.Actor.ID},
		{"result", record.Result},
	} {
		if required.value == "" {
			return AuditRecord{}, fmt.Errorf("record has no %s", required.field)
		}
	}
	if record.EventTime.IsZero() {
		return AuditRecord{}, fmt.Errorf("record has no event_time")
	}

	if v, ok := row[colNamespaceName].(string); ok {
		record.Collector.NamespaceName = v
	}
	if v, ok := row[colPodName].(string); ok {
		record.Collector.PodName = v
	}
	if v, ok := row[colContainerName].(string); ok {
		record.Collector.ContainerName = v
	}

	return record, nil
}
