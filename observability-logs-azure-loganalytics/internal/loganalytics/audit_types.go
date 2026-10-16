// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package loganalytics

import "time"

// DefaultAuditTable is a table of its own rather than the container logs,
// because in Azure retention and read access are both per-table.
const DefaultAuditTable = "OpenChoreoAuditLog_CL"

// Bounds from the adapter contract.
const (
	DefaultAuditLimit = 100
	MaxAuditLimit     = 1000

	DefaultAuditMaxValues = 100
	MaxAuditMaxValues     = 1000
)

// maxTimelineBuckets is a cap the interval is coarsened to fit, rather than a
// window being refused.
const maxTimelineBuckets = 500

// AuditParams captures an audit query before it is rendered to KQL.
//
// Multi-value fields OR within a field and AND with each other. An absent or
// empty slice is not a filter - it must not narrow the query to nothing.
//
// The Resource fields are filters over the record's own resource group, not
// scopes: the observer authorizes before the query reaches this adapter.
type AuditParams struct {
	StartTime time.Time
	EndTime   time.Time

	Limit     int
	SortOrder SortOrder

	// SearchPhrase matches the raw record line, so it also hits field names and
	// punctuation, not only values.
	SearchPhrase string

	EventIDs     []string
	RequestIDs   []string
	Actions      []string
	Categories   []string
	Results      []string
	SourceIPs    []string
	UserAgents   []string
	Producers    []string
	Surfaces     []string
	OperationIDs []string

	ActorTypes        []string
	ActorIDs          []string
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

	IncludeTimeline bool

	// TimelineInterval is "<count><unit>", unit m|h|d|w. The width used may be
	// coarser; the response reports which.
	TimelineInterval string
}

type AuditActor struct {
	Type         string
	ID           string
	Issuer       string
	SessionID    string
	Entitlements map[string][]string
}

// AuditHTTPInfo is the request line, absent for a surface that has none.
type AuditHTTPInfo struct {
	Method string
	Path   string
}

// AuditResource is the target resource and where in the tree the decision was
// authorized.
type AuditResource struct {
	Type        string
	Namespace   string
	Environment string
	Project     string
	Component   string
	Resource    string
	UID         string
	Name        string
	Metadata    map[string]any
}

// isEmpty exists because the map field makes the struct incomparable.
func (r AuditResource) isEmpty() bool {
	return r.Type == "" && r.Namespace == "" && r.Environment == "" &&
		r.Project == "" && r.Component == "" && r.Resource == "" &&
		r.UID == "" && r.Name == "" && len(r.Metadata) == 0
}

// AuditCollectorInfo is stamped by the collector, not claimed by the emitting
// service. Carried alongside Producer so the two can be compared; this adapter
// never does.
type AuditCollectorInfo struct {
	NamespaceName string
	PodName       string
	ContainerName string
}

type AuditRecord struct {
	SchemaVersion string
	EventID       string
	EventTime     time.Time
	Actor         AuditActor
	Action        string
	Category      string
	Result        string
	RequestID     string
	SourceIP      string
	UserAgent     string
	Producer      string
	Surface       string
	OperationID   string
	HTTP          *AuditHTTPInfo
	Resource      *AuditResource
	Metadata      map[string]any
	Collector     *AuditCollectorInfo
}

// AuditTimelineBucket has Counts keyed by result value; an absent key had none.
type AuditTimelineBucket struct {
	StartTime time.Time
	Total     int64
	Counts    map[string]int64
}

// AuditTimeline covers the whole window, empty buckets included: a gap would
// read as missing data rather than as no activity.
type AuditTimeline struct {
	Interval string
	Buckets  []AuditTimelineBucket
}

// AuditResult has Total counting the window, not the page.
type AuditResult struct {
	Records  []AuditRecord
	Total    int64
	Timeline *AuditTimeline
	TookMs   int
}

// AuditFilter mirrors the filter's path in the contract's AuditLogsQueryRequest.
type AuditFilter string

const (
	AuditFilterAction              AuditFilter = "action"
	AuditFilterActorEntitlements   AuditFilter = "actor.entitlements"
	AuditFilterActorID             AuditFilter = "actor.id"
	AuditFilterActorIssuer         AuditFilter = "actor.issuer"
	AuditFilterActorSessionID      AuditFilter = "actor.session_id"
	AuditFilterActorType           AuditFilter = "actor.type"
	AuditFilterCategory            AuditFilter = "category"
	AuditFilterOperationID         AuditFilter = "operation_id"
	AuditFilterProducer            AuditFilter = "producer"
	AuditFilterResourceComponent   AuditFilter = "resource.component"
	AuditFilterResourceEnvironment AuditFilter = "resource.environment"
	AuditFilterResourceName        AuditFilter = "resource.name"
	AuditFilterResourceNamespace   AuditFilter = "resource.namespace"
	AuditFilterResourceProject     AuditFilter = "resource.project"
	AuditFilterResourceResource    AuditFilter = "resource.resource"
	AuditFilterResourceType        AuditFilter = "resource.type"
	AuditFilterResult              AuditFilter = "result"
	AuditFilterSourceIP            AuditFilter = "source_ip"
	AuditFilterSurface             AuditFilter = "surface"
	AuditFilterUserAgent           AuditFilter = "user_agent"
)

// IsListableAuditFilter derives from auditFilterColumn rather than repeating
// its cases: a filter listable but without a column would render
// `| extend _value = ` and fail in Azure instead of being refused with a 400.
func IsListableAuditFilter(f AuditFilter) bool {
	return auditFilterColumn(f) != ""
}

// AuditFilterValuesParams asks for the distinct values one filter takes under
// Query. Query's selections for Filter itself are ignored - a filter that
// counted its own selection would offer only what is already picked.
type AuditFilterValuesParams struct {
	Query       AuditParams
	Filter      AuditFilter
	ValueSearch string
	MaxValues   int
}

type AuditFilterValue struct {
	Value string
	Count int64
}

type AuditFilterValuesResult struct {
	Filter      AuditFilter
	Values      []AuditFilterValue
	TotalValues int64
	TookMs      int
}
