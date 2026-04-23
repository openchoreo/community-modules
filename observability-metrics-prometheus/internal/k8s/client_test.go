// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"testing"
)

func TestClient_Namespace(t *testing.T) {
	client := &Client{
		namespace: "test-namespace",
	}

	if got := client.Namespace(); got != "test-namespace" {
		t.Errorf("Namespace() = %q, want %q", got, "test-namespace")
	}
}

// Note: Most other k8s client methods require a real Kubernetes cluster or
// a mock controller-runtime client, which would require significant test
// infrastructure. These are better tested via integration tests.
// The Namespace() method is tested here as a simple unit test.
