/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	observabilityv1alpha1 "github.com/arjunmalhotra1/aranya/api/v1alpha1"
	"github.com/arjunmalhotra1/aranya/internal/summary"
)

// ClusterViewReconciler reconciles a ClusterView object
type ClusterViewReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=observability.arjunmalhotra.dev,resources=clusterviews,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=observability.arjunmalhotra.dev,resources=clusterviews/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=observability.arjunmalhotra.dev,resources=clusterviews/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

// defaultRefreshInterval is used if spec.refreshInterval is unset. The CRD
// defaults it to the same value, so this only matters for objects created
// before that default existed.
const defaultRefreshInterval = 30 * time.Second

// Reconcile recomputes the ClusterView's status from the current nodes and
// pods, then asks to be called again after spec.refreshInterval.
func (r *ClusterViewReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var cv observabilityv1alpha1.ClusterView
	if err := r.Get(ctx, req.NamespacedName, &cv); err != nil {
		// Deleted since the event was queued: nothing to do, and no requeue.
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("get ClusterView: %w", err)
	}

	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes); err != nil {
		return ctrl.Result{}, fmt.Errorf("list nodes: %w", err)
	}
	var pods corev1.PodList
	if err := r.List(ctx, &pods); err != nil {
		return ctrl.Result{}, fmt.Errorf("list pods: %w", err)
	}

	var status observabilityv1alpha1.ClusterViewStatus
	firstTry := true
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// A conflict means the ClusterView changed after we read it (for
		// example, spec.namespaces was edited). Re-read it so the counts
		// match its latest spec, then try the write again.
		if !firstTry {
			if err := r.Get(ctx, req.NamespacedName, &cv); err != nil {
				return err
			}
		}
		firstTry = false

		status = summary.Compute(nodes.Items, pods.Items, cv.Spec.Namespaces)
		status.Conditions = cv.Status.Conditions
		status.ObservedGeneration = cv.Generation
		now := metav1.Now()
		status.LastUpdated = &now
		cv.Status = status
		return r.Status().Update(ctx, &cv)
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("update ClusterView status: %w", err)
	}

	interval := defaultRefreshInterval
	if cv.Spec.RefreshInterval != nil {
		interval = cv.Spec.RefreshInterval.Duration
	}
	log.V(1).Info("status updated", "nodes", status.NodeCount, "pods", status.PodCount, "requeueAfter", interval)
	return ctrl.Result{RequeueAfter: interval}, nil
}

// SetupWithManager sets up the controller with the Manager. Besides its own
// ClusterViews, it watches Nodes and Pods so a change in the cluster updates
// the status immediately instead of waiting for the refresh interval.
func (r *ClusterViewReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// Only spec changes bump metadata.generation. Filtering on it keeps our
		// own status writes from triggering another reconcile.
		For(&observabilityv1alpha1.ClusterView{},
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&corev1.Node{}, handler.EnqueueRequestsFromMapFunc(r.allClusterViews)).
		Watches(&corev1.Pod{}, handler.EnqueueRequestsFromMapFunc(r.allClusterViews)).
		Named("clusterview").
		Complete(r)
}

// allClusterViews maps a Node or Pod event to a request for every ClusterView,
// since each one summarizes the whole cluster. The work queue de-duplicates
// requests, so a burst of pod events collapses into one reconcile.
func (r *ClusterViewReconciler) allClusterViews(ctx context.Context, _ client.Object) []reconcile.Request {
	var views observabilityv1alpha1.ClusterViewList
	if err := r.List(ctx, &views); err != nil {
		logf.FromContext(ctx).Error(err, "list ClusterViews for Node/Pod event")
		return nil
	}
	requests := make([]reconcile.Request, 0, len(views.Items))
	for _, cv := range views.Items {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&cv)})
	}
	return requests
}
