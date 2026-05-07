// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

// Package agent implements the agent-sandbox module controller.
// It watches Component resources whose componentType resolves to the cluster-scoped
// "agent" ClusterComponentType and reconciles them by creating a RenderedRelease
// containing SandboxTemplate and SandboxClaim manifests.  The core's renderedrelease
// controller then applies those resources to the data-plane cluster via the
// cluster-gateway, ensuring multi-cluster support.
package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	openchoreov1alpha1 "github.com/openchoreo/openchoreo/api/v1alpha1"

	sandboxv1alpha1 "github.com/openchoreo/community-modules/agent-sandbox/api/v1alpha1"
)

const (
	// agentComponentTypeName is the ClusterComponentType name this controller manages.
	agentComponentTypeName = "agent"

	// agentFinalizer is added to Components so cleanup can run on deletion.
	agentFinalizer = "agent.openchoreo.dev/finalizer"

	// workloadOwnerIndex is the field index key for Workload → owner lookup.
	workloadOwnerIndex = ".spec.owner.projectName_componentName"

	// sandboxPollInterval is how often to requeue while waiting for the
	// RenderedRelease to be applied to the data plane.
	sandboxPollInterval = 15 * time.Second
)

// Reconciler reconciles Component resources that use the "agent" ComponentType.
type Reconciler struct {
	client.Client
}

// +kubebuilder:rbac:groups=openchoreo.dev,resources=components,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=openchoreo.dev,resources=components/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openchoreo.dev,resources=components/finalizers,verbs=update
// +kubebuilder:rbac:groups=openchoreo.dev,resources=clustercomponenttypes,verbs=get;list;watch
// +kubebuilder:rbac:groups=openchoreo.dev,resources=workloads,verbs=get;list;watch
// +kubebuilder:rbac:groups=openchoreo.dev,resources=projects,verbs=get;list;watch
// +kubebuilder:rbac:groups=openchoreo.dev,resources=deploymentpipelines,verbs=get;list;watch
// +kubebuilder:rbac:groups=openchoreo.dev,resources=renderedreleases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=agent.openchoreo.dev,resources=sandboxpolicies,verbs=get;list;watch

// Reconcile is the main reconciliation loop for agent Components.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	comp := &openchoreov1alpha1.Component{}
	if err := r.Get(ctx, req.NamespacedName, comp); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !isAgentComponent(comp) {
		return ctrl.Result{}, nil
	}

	if !comp.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, comp)
	}

	if added, err := r.ensureFinalizer(ctx, comp); err != nil || added {
		return ctrl.Result{}, err
	}

	return r.reconcileAgentComponent(ctx, comp)
}

// reconcileAgentComponent drives the agent workload reconciliation.
func (r *Reconciler) reconcileAgentComponent(
	ctx context.Context,
	comp *openchoreov1alpha1.Component,
) (result ctrl.Result, rErr error) {
	logger := log.FromContext(ctx)
	old := comp.DeepCopy()

	defer func() {
		comp.Status.ObservedGeneration = comp.Generation
		if apiequality.Semantic.DeepEqual(old.Status, comp.Status) {
			return
		}
		if err := r.Status().Update(ctx, comp); err != nil {
			logger.Error(err, "Failed to update Component status")
			rErr = err
		}
	}()

	// 1. Parse agent-specific parameters.
	params, err := parseAgentParams(comp)
	if err != nil {
		r.setReadyFalse(comp, ReasonInvalidConfiguration, err.Error())
		return ctrl.Result{}, nil
	}

	// 2. Fetch the Workload for this Component (contains the container image).
	workload, err := r.fetchWorkload(ctx, comp)
	if err != nil {
		return ctrl.Result{}, err
	}
	if workload == nil {
		return ctrl.Result{}, nil // condition already set
	}

	// 3. Resolve the environment name from the DeploymentPipeline.
	env, err := r.fetchFirstEnvironment(ctx, comp)
	if err != nil {
		return ctrl.Result{}, err
	}
	if env == "" {
		return ctrl.Result{}, nil // condition already set
	}

	// 4. Create/update the RenderedRelease with SandboxTemplate + SandboxClaim
	//    targeting the data-plane namespace.
	if err := r.ensureRenderedRelease(ctx, comp, params, &workload.Spec.WorkloadTemplateSpec.Container, env); err != nil {
		r.setReadyFalse(comp, ReasonReconcileError,
			fmt.Sprintf("Failed to ensure RenderedRelease: %v", err))
		return ctrl.Result{}, err
	}

	// 5. Check whether the RenderedRelease has been applied to the data plane.
	ready, msg, err := r.isRenderedReleaseReady(ctx, comp, env)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !ready {
		r.setReadyFalse(comp, ReasonSandboxClaimPending, msg)
		logger.Info(msg, "component", comp.Name)
		return ctrl.Result{RequeueAfter: sandboxPollInterval}, nil
	}

	// 6. Resources applied — mark the Component as ready.
	readyMsg := fmt.Sprintf("Agent sandbox resources applied to data plane for environment %q", env)
	r.setReadyTrue(comp, ReasonSandboxClaimBound, readyMsg)
	logger.Info("Agent Component reconciled", "component", comp.Name,
		"env", env, "isolationTier", params.IsolationTier)

	return ctrl.Result{}, nil
}

// ─── Workload ─────────────────────────────────────────────────────────────────

func (r *Reconciler) fetchWorkload(
	ctx context.Context,
	comp *openchoreov1alpha1.Component,
) (*openchoreov1alpha1.Workload, error) {
	logger := log.FromContext(ctx)
	ownerKey := fmt.Sprintf("%s/%s", comp.Spec.Owner.ProjectName, comp.Name)

	var list openchoreov1alpha1.WorkloadList
	if err := r.List(ctx, &list,
		client.InNamespace(comp.Namespace),
		client.MatchingFields{workloadOwnerIndex: ownerKey},
	); err != nil {
		return nil, err
	}
	if len(list.Items) == 0 {
		msg := fmt.Sprintf("Workload for Component %q not found; waiting", comp.Name)
		r.setReadyFalse(comp, ReasonWorkloadNotFound, msg)
		logger.Info(msg)
		return nil, nil
	}
	return &list.Items[0], nil
}

// ─── DeploymentPipeline ───────────────────────────────────────────────────────

func (r *Reconciler) fetchFirstEnvironment(
	ctx context.Context,
	comp *openchoreov1alpha1.Component,
) (string, error) {
	logger := log.FromContext(ctx)

	project := &openchoreov1alpha1.Project{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      comp.Spec.Owner.ProjectName,
		Namespace: comp.Namespace,
	}, project); err != nil {
		if apierrors.IsNotFound(err) {
			r.setReadyFalse(comp, ReasonDeploymentPipelineNotFound,
				fmt.Sprintf("Project %q not found", comp.Spec.Owner.ProjectName))
			return "", nil
		}
		return "", err
	}

	if project.Spec.DeploymentPipelineRef.Name == "" {
		r.setReadyFalse(comp, ReasonInvalidConfiguration,
			fmt.Sprintf("Project %q has no deploymentPipelineRef", project.Name))
		return "", nil
	}

	pipeline := &openchoreov1alpha1.DeploymentPipeline{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      project.Spec.DeploymentPipelineRef.Name,
		Namespace: project.Namespace,
	}, pipeline); err != nil {
		if apierrors.IsNotFound(err) {
			r.setReadyFalse(comp, ReasonDeploymentPipelineNotFound,
				fmt.Sprintf("DeploymentPipeline %q not found", project.Spec.DeploymentPipelineRef.Name))
			return "", nil
		}
		return "", err
	}

	firstEnv, err := findRootEnvironment(pipeline)
	if err != nil {
		msg := fmt.Sprintf("Invalid deployment pipeline: %v", err)
		r.setReadyFalse(comp, ReasonInvalidConfiguration, msg)
		logger.Info(msg)
		return "", nil
	}
	return firstEnv, nil
}

func findRootEnvironment(pipeline *openchoreov1alpha1.DeploymentPipeline) (string, error) {
	if len(pipeline.Spec.PromotionPaths) == 0 {
		return "", fmt.Errorf("deployment pipeline %s has no promotion paths", pipeline.Name)
	}
	targets := make(map[string]bool)
	for _, path := range pipeline.Spec.PromotionPaths {
		for _, t := range path.TargetEnvironmentRefs {
			targets[t.Name] = true
		}
	}
	for _, path := range pipeline.Spec.PromotionPaths {
		if path.SourceEnvironmentRef.Name != "" && !targets[path.SourceEnvironmentRef.Name] {
			return path.SourceEnvironmentRef.Name, nil
		}
	}
	return "", fmt.Errorf("deployment pipeline %s has no root environment", pipeline.Name)
}

// ─── Finalizer ────────────────────────────────────────────────────────────────

func (r *Reconciler) ensureFinalizer(ctx context.Context, comp *openchoreov1alpha1.Component) (bool, error) {
	for _, f := range comp.Finalizers {
		if f == agentFinalizer {
			return false, nil
		}
	}
	comp.Finalizers = append(comp.Finalizers, agentFinalizer)
	if err := r.Update(ctx, comp); err != nil {
		return false, fmt.Errorf("failed to add finalizer: %w", err)
	}
	return true, nil
}

func (r *Reconciler) finalize(ctx context.Context, comp *openchoreov1alpha1.Component) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	hasFinalizer := false
	for _, f := range comp.Finalizers {
		if f == agentFinalizer {
			hasFinalizer = true
			break
		}
	}
	if !hasFinalizer {
		return ctrl.Result{}, nil
	}

	logger.Info("Finalizing agent Component", "component", comp.Name)

	// Clean up the RenderedRelease — the renderedrelease controller will handle
	// deleting the data-plane resources.
	if err := r.cleanupRenderedRelease(ctx, comp); err != nil {
		return ctrl.Result{}, err
	}

	// Remove our finalizer.
	finalizers := make([]string, 0, len(comp.Finalizers))
	for _, f := range comp.Finalizers {
		if f != agentFinalizer {
			finalizers = append(finalizers, f)
		}
	}
	comp.Finalizers = finalizers
	if err := r.Update(ctx, comp); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func isAgentComponent(comp *openchoreov1alpha1.Component) bool {
	name := comp.Spec.ComponentType.Name
	kind := comp.Spec.ComponentType.Kind
	return strings.HasSuffix(name, "/"+agentComponentTypeName) &&
		kind == openchoreov1alpha1.ComponentTypeRefKindClusterComponentType
}

func (r *Reconciler) setReadyTrue(comp *openchoreov1alpha1.Component, reason, msg string) {
	setCondition(&comp.Status.Conditions,
		agentSandboxReadyCondition(metav1.ConditionTrue, reason, msg, comp.Generation))
}

func (r *Reconciler) setReadyFalse(comp *openchoreov1alpha1.Component, reason, msg string) {
	setCondition(&comp.Status.Conditions,
		agentSandboxReadyCondition(metav1.ConditionFalse, reason, msg, comp.Generation))
}

// ─── SetupWithManager ─────────────────────────────────────────────────────────

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	ctx := context.Background()

	// Index Workloads by "projectName/componentName" for fast owner lookup.
	if err := mgr.GetFieldIndexer().IndexField(ctx,
		&openchoreov1alpha1.Workload{},
		workloadOwnerIndex,
		func(obj client.Object) []string {
			w := obj.(*openchoreov1alpha1.Workload)
			return []string{fmt.Sprintf("%s/%s",
				w.Spec.Owner.ProjectName, w.Spec.Owner.ComponentName)}
		},
	); err != nil {
		return fmt.Errorf("failed to set up workload owner index: %w", err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&openchoreov1alpha1.Component{}).
		Watches(&openchoreov1alpha1.ClusterComponentType{},
			handler.EnqueueRequestsFromMapFunc(r.findComponentsForClusterComponentType)).
		Watches(&openchoreov1alpha1.Workload{},
			handler.EnqueueRequestsFromMapFunc(r.findComponentsForWorkload)).
		Watches(&sandboxv1alpha1.SandboxPolicy{},
			handler.EnqueueRequestsFromMapFunc(r.findComponentsForSandboxPolicy)).
		// Requeue when the RenderedRelease status changes (resources applied).
		Watches(&openchoreov1alpha1.RenderedRelease{},
			handler.EnqueueRequestsFromMapFunc(r.findComponentsForRenderedRelease)).
		WithEventFilter(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			comp, ok := obj.(*openchoreov1alpha1.Component)
			if !ok {
				return true
			}
			return isAgentComponent(comp)
		})).
		Named("agent-sandbox").
		Complete(r)
}

// ─── Watch mappers ────────────────────────────────────────────────────────────

func (r *Reconciler) findComponentsForClusterComponentType(
	ctx context.Context, obj client.Object,
) []ctrl.Request {
	if obj.GetName() != agentComponentTypeName {
		return nil
	}
	var list openchoreov1alpha1.ComponentList
	if err := r.List(ctx, &list); err != nil {
		return nil
	}
	var reqs []ctrl.Request
	for _, comp := range list.Items {
		if isAgentComponent(&comp) {
			reqs = append(reqs, ctrl.Request{NamespacedName: types.NamespacedName{
				Name:      comp.Name,
				Namespace: comp.Namespace,
			}})
		}
	}
	return reqs
}

func (r *Reconciler) findComponentsForWorkload(
	ctx context.Context, obj client.Object,
) []ctrl.Request {
	workload, ok := obj.(*openchoreov1alpha1.Workload)
	if !ok {
		return nil
	}
	var list openchoreov1alpha1.ComponentList
	if err := r.List(ctx, &list, client.InNamespace(workload.Namespace)); err != nil {
		return nil
	}
	var reqs []ctrl.Request
	for _, comp := range list.Items {
		if isAgentComponent(&comp) &&
			comp.Spec.Owner.ProjectName == workload.Spec.Owner.ProjectName &&
			comp.Name == workload.Spec.Owner.ComponentName {
			reqs = append(reqs, ctrl.Request{NamespacedName: types.NamespacedName{
				Name:      comp.Name,
				Namespace: comp.Namespace,
			}})
		}
	}
	return reqs
}

func (r *Reconciler) findComponentsForSandboxPolicy(
	ctx context.Context, obj client.Object,
) []ctrl.Request {
	policyName := obj.GetName()
	var list openchoreov1alpha1.ComponentList
	if err := r.List(ctx, &list, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}
	var reqs []ctrl.Request
	for _, comp := range list.Items {
		if !isAgentComponent(&comp) {
			continue
		}
		if sandboxPolicyRefFromComp(&comp) == policyName {
			reqs = append(reqs, ctrl.Request{NamespacedName: types.NamespacedName{
				Name:      comp.Name,
				Namespace: comp.Namespace,
			}})
		}
	}
	return reqs
}

func sandboxPolicyRefFromComp(comp *openchoreov1alpha1.Component) string {
	if comp.Spec.Parameters == nil {
		return ""
	}
	raw := string(comp.Spec.Parameters.Raw)
	const key = `"sandboxPolicyRef"`
	idx := strings.Index(raw, key)
	if idx < 0 {
		return ""
	}
	rest := raw[idx+len(key):]
	colonIdx := strings.Index(rest, ":")
	if colonIdx < 0 {
		return ""
	}
	rest = strings.TrimSpace(rest[colonIdx+1:])
	if len(rest) == 0 || rest[0] != '"' {
		return ""
	}
	end := strings.Index(rest[1:], `"`)
	if end < 0 {
		return ""
	}
	return rest[1 : end+1]
}
