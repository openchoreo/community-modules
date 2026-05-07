// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"encoding/json"
	"fmt"

	openchoreov1alpha1 "github.com/openchoreo/openchoreo/api/v1alpha1"
)

// IsolationTier defines the container runtime for sandbox isolation.
type IsolationTier string

const (
	// IsolationRunc uses standard Linux container isolation (namespaces + cgroups).
	IsolationRunc IsolationTier = "runc"
	// IsolationGVisor uses gVisor for syscall interception via a user-space kernel.
	IsolationGVisor IsolationTier = "gvisor"
	// IsolationKata uses Kata Containers for full VM isolation via Firecracker/QEMU.
	IsolationKata IsolationTier = "kata"
)

// agentParams holds the validated, typed parameters for an agent Component.
type agentParams struct {
	// IsolationTier controls the sandbox runtime (runc | gvisor | kata).
	IsolationTier IsolationTier `json:"isolationTier,omitempty"`
	// SandboxPolicyRef is the name of the SandboxPolicy to use for network egress control.
	SandboxPolicyRef string `json:"sandboxPolicyRef,omitempty"`
	// WarmPoolSize is the number of pre-warmed sandbox instances. 0 means no warm pool.
	WarmPoolSize int32 `json:"warmPoolSize,omitempty"`
	// TTLSeconds is the time-to-live for the sandbox after being claimed. 0 means no expiry.
	TTLSeconds int32 `json:"ttlSeconds,omitempty"`
}

// parseAgentParams unmarshals the Component's raw parameters into an agentParams struct.
// Missing fields default to: IsolationTier → "runc".
func parseAgentParams(comp *openchoreov1alpha1.Component) (*agentParams, error) {
	p := &agentParams{
		IsolationTier: IsolationRunc,
	}
	if comp.Spec.Parameters == nil {
		return p, nil
	}
	if err := json.Unmarshal(comp.Spec.Parameters.Raw, p); err != nil {
		return nil, fmt.Errorf("invalid agent parameters: %w", err)
	}
	if p.IsolationTier == "" {
		p.IsolationTier = IsolationRunc
	}
	switch p.IsolationTier {
	case IsolationRunc, IsolationGVisor, IsolationKata:
		// valid
	default:
		return nil, fmt.Errorf("unsupported isolationTier %q: must be one of runc, gvisor, kata", p.IsolationTier)
	}
	return p, nil
}

// runtimeClassName maps an IsolationTier to the Kubernetes runtimeClassName.
// Returns an empty string for IsolationRunc (uses the cluster default runtime).
func runtimeClassName(tier IsolationTier) string {
	switch tier {
	case IsolationGVisor:
		return "gvisor"
	case IsolationKata:
		return "kata"
	default:
		return ""
	}
}
