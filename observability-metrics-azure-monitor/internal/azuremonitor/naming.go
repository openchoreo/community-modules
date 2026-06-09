// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package azuremonitor

import (
	"crypto/sha256"
	"encoding/hex"
)

const (
	azureNamePrefix               = "oc-"
	CustomPropOpenChoreoNamespace = "openchoreo-namespace"
	CustomPropOpenChoreoRuleName  = "openchoreo-rule-name"
)

func DeriveAzureName(namespace, ruleName string) string {
	h := sha256.Sum256([]byte(namespace + "/" + ruleName))
	return azureNamePrefix + hex.EncodeToString(h[:16])
}
