// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package azuremonitor

import (
	"crypto/sha256"
	"encoding/hex"
)

const (
	// azureNamePrefix marks rules created by this adapter.
	azureNamePrefix = "oc-"

	// CustomPropOpenChoreoNamespace carries the OpenChoreo namespace of the
	// rule through Common Alert Schema's data.customProperties.
	CustomPropOpenChoreoNamespace = "openchoreo-namespace"

	// CustomPropOpenChoreoRuleName carries the OpenChoreo rule name of the
	// rule through Common Alert Schema's data.customProperties.
	CustomPropOpenChoreoRuleName = "openchoreo-rule-name"
)

// DeriveAzureName produces a deterministic Azure resource name from the
// OpenChoreo (namespace, ruleName) pair. The Azure rule-name regex is
// ^[^#<>%&:?/{}*]{1,260}$ — a hex digest fits comfortably and avoids the
// disallowed characters entirely.
//
// The OpenChoreo identity is NOT recoverable from this name alone; it is
// carried through customProperties on the rule (see CustomProp* constants).
func DeriveAzureName(namespace, ruleName string) string {
	h := sha256.Sum256([]byte(namespace + "/" + ruleName))
	return azureNamePrefix + hex.EncodeToString(h[:16])
}
