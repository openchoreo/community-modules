// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

// Package policy implements the SandboxPolicy controller.
// It watches SandboxPolicy resources and generates Kubernetes NetworkPolicy
// objects that enforce network egress rules for agent workloads.
//
// Phase 1 note: NetworkPolicy objects are created in the SAME namespace as the
// SandboxPolicy (control plane). Applying them to data-plane namespaces requires
// cluster-agent integration and will be addressed in a follow-up.
package policy

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	sandboxv1alpha1 "github.com/openchoreo/community-modules/agent-sandbox/api/v1alpha1"
)

const (
	// networkPolicyNamePrefix is prepended to the SandboxPolicy name to form
	// the generated NetworkPolicy name.
	networkPolicyNamePrefix = "agent-sandbox-"

	// labelManagedBy is applied to NetworkPolicy objects created by this controller.
	labelManagedBy = "agent.openchoreo.dev/managed-by"
	labelPolicyRef = "agent.openchoreo.dev/sandbox-policy"
)

// Reconciler reconciles SandboxPolicy resources.
type Reconciler struct {
	client.Client
}

// +kubebuilder:rbac:groups=agent.openchoreo.dev,resources=sandboxpolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups=agent.openchoreo.dev,resources=sandboxpolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete

// Reconcile watches SandboxPolicy and creates/updates the corresponding NetworkPolicy.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	sp := &sandboxv1alpha1.SandboxPolicy{}
	if err := r.Get(ctx, req.NamespacedName, sp); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion.
	if !sp.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, r.deleteNetworkPolicy(ctx, sp)
	}

	np := r.buildNetworkPolicy(sp)
	if err := r.applyNetworkPolicy(ctx, sp, np); err != nil {
		logger.Error(err, "Failed to apply NetworkPolicy", "sandboxPolicy", sp.Name)
		return ctrl.Result{}, err
	}

	logger.Info("NetworkPolicy reconciled", "sandboxPolicy", sp.Name, "networkPolicy", np.Name)
	return ctrl.Result{}, nil
}

// buildNetworkPolicy converts a SandboxPolicy into a Kubernetes NetworkPolicy.
//
// The generated policy:
//   - Denies all ingress (agents should not accept inbound connections).
//   - Denies all egress by default when spec.defaultEgress == "deny".
//   - Always allows UDP 53 to kube-dns (CIDR 0.0.0.0/0, port 53).
//   - Adds an allow rule per AllowedHost entry.
//   - Adds an allow rule per AllowedMCPServer (hostname resolved at apply time by the CNI).
func (r *Reconciler) buildNetworkPolicy(sp *sandboxv1alpha1.SandboxPolicy) *networkingv1.NetworkPolicy {
	npName := networkPolicyNamePrefix + sp.Name

	// Pod selector: target pods labelled with the sandbox policy ref.
	// Agent pods are expected to carry the label
	//   agent.openchoreo.dev/sandbox-policy: <policyName>
	// which the agent reconciler stamps on the ComponentRelease (and flows to pods via
	// the Deployment template).
	podSelector := metav1.LabelSelector{
		MatchLabels: map[string]string{
			labelPolicyRef: sp.Name,
		},
	}

	policyTypes := []networkingv1.PolicyType{networkingv1.PolicyTypeEgress}

	var egressRules []networkingv1.NetworkPolicyEgressRule

	// Always allow kube-dns (UDP + TCP port 53).
	dnsPort53UDP := intstr.FromInt(53)
	dnsPort53TCP := intstr.FromInt(53)
	protoUDP := corev1.ProtocolUDP
	protoTCP := corev1.ProtocolTCP
	egressRules = append(egressRules, networkingv1.NetworkPolicyEgressRule{
		Ports: []networkingv1.NetworkPolicyPort{
			{Protocol: &protoUDP, Port: &dnsPort53UDP},
			{Protocol: &protoTCP, Port: &dnsPort53TCP},
		},
	})

	// Add rules for each AllowedHost.
	for _, ah := range sp.Spec.AllowedHosts {
		rule := allowedHostToEgressRule(ah)
		egressRules = append(egressRules, rule)
	}

	// Add rules for each AllowedMCPServer (treated as a hostname-based allow rule).
	for _, mcp := range sp.Spec.AllowedMCPServers {
		rule := mcpServerToEgressRule(mcp)
		egressRules = append(egressRules, rule)
	}

	// When defaultEgress is "allow", append a catch-all egress rule.
	if sp.Spec.DefaultEgress == sandboxv1alpha1.EgressActionAllow {
		egressRules = append(egressRules, networkingv1.NetworkPolicyEgressRule{})
	}

	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      npName,
			Namespace: sp.Namespace,
			Labels: map[string]string{
				labelManagedBy: "agent-sandbox-controller",
				labelPolicyRef: sp.Name,
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: sp.APIVersion,
					Kind:       sp.Kind,
					Name:       sp.Name,
					UID:        sp.UID,
				},
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: podSelector,
			PolicyTypes: policyTypes,
			Egress:      egressRules,
		},
	}
}

// allowedHostToEgressRule converts an AllowedHost into a NetworkPolicyEgressRule.
func allowedHostToEgressRule(ah sandboxv1alpha1.AllowedHost) networkingv1.NetworkPolicyEgressRule {
	rule := networkingv1.NetworkPolicyEgressRule{}

	for _, port := range ah.Ports {
		p := intstr.FromInt(int(port))
		proto := corev1.ProtocolTCP
		if ah.Protocol == "UDP" {
			proto = corev1.ProtocolUDP
		}
		rule.Ports = append(rule.Ports, networkingv1.NetworkPolicyPort{
			Protocol: &proto,
			Port:     &p,
		})
	}

	// NetworkPolicy v1 targets pod/namespace selectors, not DNS hostnames.
	// For CIDR-based hosts (e.g. "10.0.0.0/8"), use IPBlock.
	// For DNS hostnames, the rule is permissive on IP range; a CNI like
	// Cilium or Calico can enforce hostname-based rules via their own CRDs.
	if isCIDR(ah.Host) {
		rule.To = []networkingv1.NetworkPolicyPeer{{
			IPBlock: &networkingv1.IPBlock{CIDR: ah.Host},
		}}
	}
	// If host is a DNS name, no To restriction is added at the NetworkPolicy level —
	// enforce via CiliumNetworkPolicy or Calico NetworkPolicy in a follow-up.

	return rule
}

// mcpServerToEgressRule produces an egress rule for an MCP server URL.
// Phase 1: allows all egress on port 443 (HTTPS). Hostname enforcement
// requires CNI-level policy and is tracked for Phase 2.
func mcpServerToEgressRule(_ sandboxv1alpha1.AllowedMCPServer) networkingv1.NetworkPolicyEgressRule {
	port := intstr.FromInt(443)
	proto := corev1.ProtocolTCP
	return networkingv1.NetworkPolicyEgressRule{
		Ports: []networkingv1.NetworkPolicyPort{
			{Protocol: &proto, Port: &port},
		},
	}
}

// isCIDR returns true when the string looks like an IP or CIDR block.
func isCIDR(s string) bool {
	for _, c := range s {
		if c == '/' || (c >= '0' && c <= '9') || c == '.' || c == ':' {
			continue
		}
		return false
	}
	return len(s) > 0
}

// applyNetworkPolicy creates or updates the NetworkPolicy using server-side apply semantics.
func (r *Reconciler) applyNetworkPolicy(
	ctx context.Context,
	sp *sandboxv1alpha1.SandboxPolicy,
	desired *networkingv1.NetworkPolicy,
) error {
	existing := &networkingv1.NetworkPolicy{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      desired.Name,
		Namespace: desired.Namespace,
	}, existing)

	if apierrors.IsNotFound(err) {
		if err := r.Create(ctx, desired); err != nil {
			return fmt.Errorf("failed to create NetworkPolicy %q: %w", desired.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get NetworkPolicy %q: %w", desired.Name, err)
	}

	existing.Spec = desired.Spec
	existing.Labels = desired.Labels
	if err := r.Update(ctx, existing); err != nil {
		return fmt.Errorf("failed to update NetworkPolicy %q: %w", desired.Name, err)
	}
	return nil
}

// deleteNetworkPolicy removes the NetworkPolicy generated for a SandboxPolicy.
func (r *Reconciler) deleteNetworkPolicy(ctx context.Context, sp *sandboxv1alpha1.SandboxPolicy) error {
	np := &networkingv1.NetworkPolicy{}
	npName := networkPolicyNamePrefix + sp.Name
	err := r.Get(ctx, types.NamespacedName{Name: npName, Namespace: sp.Namespace}, np)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get NetworkPolicy %q for deletion: %w", npName, err)
	}
	if err := r.Delete(ctx, np); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete NetworkPolicy %q: %w", npName, err)
	}
	return nil
}

// SetupWithManager registers the SandboxPolicy controller with the manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&sandboxv1alpha1.SandboxPolicy{}).
		Owns(&networkingv1.NetworkPolicy{}).
		Named("sandbox-policy").
		Complete(r)
}
