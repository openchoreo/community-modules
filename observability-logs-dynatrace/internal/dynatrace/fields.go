// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package dynatrace

// Grail field names this adapter reads.
//
// Container logs and audit records are shaped by the module's Fluent Bit Lua filter
// (helm/templates/fluent-bit/config.yaml), which flattens Fluent Bit's nested Kubernetes
// metadata into the flat keys below before shipping to the log ingest API. The two sides
// are one contract: a field renamed here must be renamed there.
//
// Kubernetes events are shipped by the observability-events-otel-collector module over
// OTLP, which Dynatrace stores with attribute keys unchanged.
const (
	fTimestamp = "timestamp"
	fContent   = "content"
	fLogSource = "log.source"
	fLogLevel  = "loglevel"

	// Kubernetes coordinates, named as in the Dynatrace semantic dictionary.
	fClusterName    = "k8s.cluster.name"
	fNamespaceName  = "k8s.namespace.name"
	fPodName        = "k8s.pod.name"
	fContainerName  = "k8s.container.name"
	fNodeName       = "k8s.node.name"
	fPodIP          = "k8s.pod.ip"
	fContainerImage = "container.image.name"

	// fPodLabels holds every pod label as a "key=value" string. An array of strings keeps
	// each label key exactly as Kubernetes spells it, where turning keys into field names
	// would have to mangle the "/" and "." they contain.
	fPodLabels = "k8s.pod.labels"

	// OpenChoreo labels copied onto their own fields, so the component and workflow log
	// queries filter on a plain field instead of searching the label array.
	fOCNamespace      = "openchoreo.namespace"
	fOCProject        = "openchoreo.project"
	fOCProjectUID     = "openchoreo.project_uid"
	fOCComponent      = "openchoreo.component"
	fOCComponentUID   = "openchoreo.component_uid"
	fOCEnvironment    = "openchoreo.environment"
	fOCEnvironmentUID = "openchoreo.environment_uid"
	fOCWorkflowRun    = "openchoreo.workflow_run"
)

// Kubernetes event fields, as written by the OTel k8s_events receiver and the
// k8seventenrich processor.
const (
	evReason          = "k8s.event.reason"
	evObjectKind      = "k8s.object.kind"
	evObjectName      = "k8s.object.name"
	evObjectNamespace = "k8s.namespace.name"
	evSeverity        = "severity"

	evLabelPrefix     = "k8s.object.label.openchoreo.dev/"
	evComponentName   = evLabelPrefix + "component"
	evComponentID     = evLabelPrefix + "component-uid"
	evProjectName     = evLabelPrefix + "project"
	evProjectID       = evLabelPrefix + "project-uid"
	evEnvironmentName = evLabelPrefix + "environment"
	evEnvironmentID   = evLabelPrefix + "environment-uid"
	evNamespaceName   = evLabelPrefix + "namespace"
)

// Audit record fields. Every filterable field of the record is lifted to its own field
// under the "audit." prefix; the record itself is kept verbatim in content and parsed back
// from there, so nothing is lost to flattening.
const (
	auEventID     = "audit.event_id"
	auAction      = "audit.action"
	auCategory    = "audit.category"
	auResult      = "audit.result"
	auRequestID   = "audit.request_id"
	auSourceIP    = "audit.source_ip"
	auUserAgent   = "audit.user_agent"
	auProducer    = "audit.producer"
	auSurface     = "audit.surface"
	auOperationID = "audit.operation_id"

	auActorType      = "audit.actor.type"
	auActorID        = "audit.actor.id"
	auActorIssuer    = "audit.actor.issuer"
	auActorSessionID = "audit.actor.session_id"
	// auActorEntitlements holds every value of every entitlement claim, so an entitlement
	// filter matches regardless of which claim carries it.
	auActorEntitlements = "audit.actor.entitlements"

	auResourceType        = "audit.resource.type"
	auResourceNamespace   = "audit.resource.namespace"
	auResourceEnvironment = "audit.resource.environment"
	auResourceProject     = "audit.resource.project"
	auResourceComponent   = "audit.resource.component"
	auResourceResource    = "audit.resource.resource"
	auResourceName        = "audit.resource.name"
)
