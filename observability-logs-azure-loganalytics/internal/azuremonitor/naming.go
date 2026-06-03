// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package azuremonitor

import (
	"crypto/sha256"
	"encoding/hex"
)

const (
	azureNamePrefix = "oc-"

	// CustomPropOpenChoreoNamespace carries the OpenChoreo namespace of the
	// rule through Common Alert Schema's data.customProperties.
	CustomPropOpenChoreoNamespace = "openchoreo-namespace"

	// CustomPropOpenChoreoRuleName carries the OpenChoreo rule name of the
	// rule through Common Alert Schema's data.customProperties.
	CustomPropOpenChoreoRuleName = "openchoreo-rule-name"
)

// DeriveAzureName produces a deterministic Azure resource name from the
// OpenChoreo (namespace, ruleName) pair. The name is a truncated SHA-256 hash of the pair, with an "oc-" prefix
func DeriveAzureName(namespace, ruleName string) string {
	h := sha256.Sum256([]byte(namespace + "/" + ruleName))
	return azureNamePrefix + hex.EncodeToString(h[:16])
}
