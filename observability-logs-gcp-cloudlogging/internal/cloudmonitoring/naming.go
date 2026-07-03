// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudmonitoring

import (
	"crypto/sha256"
	"encoding/hex"
)

const (
	// resourceNamePrefix prefixes the deterministic GCP resource identifiers
	// derived from an OpenChoreo (namespace, ruleName) pair.
	resourceNamePrefix = "oc-"

	// UserLabelNamespace and UserLabelRuleName are the alert-policy user_labels
	// the adapter stamps so it can look a policy back up (and recover the
	// OpenChoreo identity from a firing webhook via incident.policy_user_labels).
	//
	// GCP label KEYS must match [a-z][a-z0-9_-]* (<=63 chars), so dots are not
	// allowed — hence "openchoreo-namespace" / "openchoreo-rule-name", not the
	// dotted openchoreo.dev form.
	UserLabelNamespace = "openchoreo-namespace"
	UserLabelRuleName  = "openchoreo-rule-name"

	// UserLabelManagedBy marks policies and metrics this adapter owns.
	UserLabelManagedBy = "managed-by"
	ManagedByValue     = "openchoreo"

	// UserLabelRuleID is the collision-free identity: the deriveResourceName
	// anchor (oc-<sha>) of the (namespace, ruleName) pair. openchoreo-rule-name
	// is truncated to GCP's 63-char limit and can collide for long names that
	// share a prefix, so create/delete look up by this anchor instead. Its value
	// is always [a-z0-9-], so it also carries no filter-injection surface.
	UserLabelRuleID = "openchoreo-rule-id"
)

func deriveResourceName(namespace, ruleName string) string {
	h := sha256.Sum256([]byte(namespace + "/" + ruleName))
	return resourceNamePrefix + hex.EncodeToString(h[:16])
}
