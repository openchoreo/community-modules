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

	// UserLabelRuleNameKey is the collision-resistant name-only lookup key: the
	// truncated rule name plus a hash of the full name (see labelRuleName). It
	// exists separately from UserLabelRuleName because that plain label is the
	// human-readable identity forwarded on the webhook path and must stay the
	// real name, whereas name-only lookup needs a value that can't collide when
	// two long names share a 63-byte prefix.
	UserLabelRuleNameKey = "openchoreo-rule-name-key"

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

// ruleNameHashLen is the number of hex chars of the full-name hash appended to
// the openchoreo-rule-name label. 10 hex = 40 bits — collision-negligible here.
const ruleNameHashLen = 10

// labelRuleName builds the collision-resistant openchoreo-rule-name-key label
// value for a rule. GCP label values are capped at 63 bytes, so a long ruleName
// must be truncated — but plain truncation makes two names that share a 63-byte
// prefix (e.g. "<60 chars>-primary" / "<60 chars>-secondary") map to the same
// value, so a name-only lookup could match the wrong policy. Appending a short
// hash of the FULL name keeps the value <=63 bytes and human-readable while
// making it collision-resistant. Write and lookup must use this identically.
func labelRuleName(ruleName string) string {
	h := sha256.Sum256([]byte(ruleName))
	suffix := "-" + hex.EncodeToString(h[:])[:ruleNameHashLen]
	prefix := sanitizeLabelValueMax(ruleName, 63-len(suffix))
	return prefix + suffix
}
