// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package loganalytics

// Columns of the audit table.
//
// The same names appear in three places that nothing checks against each other:
// here, the --columns list in scripts/setup-audit.sh, and the project list in
// scripts/audit-transform.kql. Keep them in step by hand. A mismatch fails
// silently, as a column that is always empty or a filter that matches nothing.
//
// Azure reserves Type, id, TenantId, UniqueId, Title, _ResourceId and
// _SubscriptionId, hence the prefixed ActorType and ResourceType.
const (
	// ColTimeGenerated carries event_time. It is the partition column, so
	// ranging on anything else scans the whole retention window.
	ColTimeGenerated = "TimeGenerated"

	// ColEventTime is event_time verbatim. Azure overwrites TimeGenerated with
	// the arrival time when the value is over two days old, so this is what
	// survives a replay, and the two differing identifies such a record.
	ColEventTime = "EventTime"

	// ColCollectedTime is the collector's clock, captured before TimeGenerated is
	// overwritten with event_time. Stored rather than projected: the contract has
	// no field for it, and it makes ingestion lag visible to a direct query.
	ColCollectedTime = "CollectedTime"

	ColSchemaVersion = "SchemaVersion"
	ColEventID       = "EventId"
	ColAction        = "Action"
	ColCategory      = "Category"
	ColResult        = "Result"
	ColRequestID     = "RequestId"
	ColSourceIP      = "SourceIP"
	ColUserAgent     = "UserAgent"
	ColSurface       = "Surface"
	ColOperationID   = "OperationId"

	// ColProducer is the emitting service's own claim, unlike the Collector*
	// columns below.
	ColProducer = "Producer"

	ColActorType      = "ActorType"
	ColActorID        = "ActorId"
	ColActorIssuer    = "ActorIssuer"
	ColActorSessionID = "ActorSessionId"

	// ColActorEntitlements holds actor.entitlements whole. Claim names vary by
	// subject, so queries walk it with bag_keys. Flattening it in the DCR
	// transform is not an option, as transformations have no mv-expand.
	ColActorEntitlements = "ActorEntitlements"

	ColHTTPMethod = "HttpMethod"
	ColHTTPPath   = "HttpPath"

	ColResourceType        = "ResourceType"
	ColResourceNamespace   = "ResourceNamespace"
	ColResourceEnvironment = "ResourceEnvironment"
	ColResourceProject     = "ResourceProject"
	ColResourceComponent   = "ResourceComponent"
	ColResourceResource    = "ResourceResource"
	ColResourceUID         = "ResourceUid"
	ColResourceName        = "ResourceName"
	ColResourceMetadata    = "ResourceMetadata"

	ColMetadata = "Metadata"

	// Collector* come from the kubelet, never from the producer's line. That is
	// the trust boundary: a pod cannot claim to be another pod.
	ColCollectorNamespace = "CollectorNamespace"
	ColCollectorPod       = "CollectorPod"
	ColCollectorContainer = "CollectorContainer"

	// ColClusterInstance is a per-cluster literal in the transform, so unlike
	// Producer a pod cannot forge it. Deliberately not in auditProjection: the
	// audit contract's collector object carries namespace, pod and container and
	// has no cluster field, so there is nowhere to return it. Stored for direct
	// KQL until the contract gains one. The OpenSearch and OpenObserve modules
	// store an equivalent and cannot return it either.
	ColClusterInstance = "ClusterInstance"

	// ColRawRecord is the producer's line as emitted. Azure drops fields the
	// table does not declare, so a field added by a later schema_version
	// survives here. searchPhrase scans it.
	ColRawRecord = "RawRecord"
)

// Names the queries produce rather than read: projection aliases and aggregate
// outputs.
const (
	ColActorEntitlementsJSON = "ActorEntitlementsJson"
	ColMetadataJSON          = "MetadataJson"
	ColResourceMetadataJSON  = "ResourceMetadataJson"

	ColTotal       = "Total"
	ColBucketStart = "BucketStart"
	ColBucketTotal = "BucketTotal"

	ColValue       = "Value"
	ColCount       = "Count"
	ColTotalValues = "TotalValues"
)

// auditIdentityColumns identify an event. The query filters on these, not just
// the mapping: a row skipped only at mapping time is still counted by Total and
// the timeline, so a page of 9 would report a total of 10.
//
// ColEventTime is checked separately, being a datetime.
var auditIdentityColumns = []string{
	ColSchemaVersion,
	ColEventID,
	ColActorType,
	ColActorID,
	ColResult,
}
