// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

// names.go replicates the OpenChoreo data-plane naming convention so the module
// can compute the target namespace (dp-{ns}-{project}-{env}-{hash}) without
// importing internal/ packages from the core.

package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
)

const (
	maxNamespaceNameLength = 63
	hashLength             = 8
	nameSeparator          = "-"
)

// generateDPNamespace returns the data-plane namespace name that the environment
// controller creates for a given (controlPlaneNamespace, project, environment) tuple.
// Format: dp-{namespace}-{project}-{env}-{hash}
func generateDPNamespace(namespaceName, projectName, environmentName string) string {
	return generateK8sNameWithLimit(maxNamespaceNameLength,
		"dp", namespaceName, projectName, environmentName)
}

// generateResourceName returns a Kubernetes-safe name for a data-plane resource.
func generateResourceName(componentName, environmentName string) string {
	return generateK8sNameWithLimit(253, componentName, environmentName)
}

// generateK8sNameWithLimit mirrors the core's GenerateK8sNameWithLengthLimit.
func generateK8sNameWithLimit(limit int, names ...string) string {
	cleaned := make([]string, 0, len(names))
	for _, n := range names {
		cleaned = append(cleaned, sanitizeName(n))
	}

	full := strings.Join(names, nameSeparator)
	h := sha256.Sum256([]byte(full))
	hash := hex.EncodeToString(h[:])[:hashLength]

	numNames := len(cleaned)
	sepLen := (numNames - 1 + 1) * len(nameSeparator) // between parts + before hash
	maxBase := limit - hashLength - sepLen

	partLen := maxBase / numNames
	extra := maxBase % numNames

	truncated := make([]string, numNames)
	for i, name := range cleaned {
		alloc := partLen
		if i < extra {
			alloc++
		}
		if len(name) > alloc {
			truncated[i] = name[:alloc]
		} else {
			truncated[i] = name
		}
	}

	final := fmt.Sprintf("%s%s%s", strings.Join(truncated, nameSeparator), nameSeparator, hash)
	return ensureDNSCompliance(final)
}

func sanitizeName(name string) string {
	name = strings.ToLower(name)
	var out []rune
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '-' || r == '.' {
			out = append(out, r)
		} else {
			out = append(out, '-')
		}
	}
	return strings.Trim(string(out), "-.")
}

func ensureDNSCompliance(name string) string {
	name = strings.TrimLeftFunc(name, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	name = strings.TrimRightFunc(name, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	return name
}
