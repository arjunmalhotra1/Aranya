// Package summary turns lists of nodes and pods into ClusterView status counts.
// It is pure: no API calls, no clock, so it is fully unit-testable.
package summary

import (
	"cmp"
	"slices"

	corev1 "k8s.io/api/core/v1"

	observabilityv1alpha1 "github.com/arjunmalhotra1/aranya/api/v1alpha1"
)

// Compute returns the count fields of a ClusterViewStatus for the given nodes
// and pods. Pods outside namespaces are skipped; an empty namespaces list
// selects all pods. Conditions, observedGeneration and lastUpdated are left
// unset for the reconciler to fill in.
func Compute(nodes []corev1.Node, pods []corev1.Pod, namespaces []string) observabilityv1alpha1.ClusterViewStatus {
	var status observabilityv1alpha1.ClusterViewStatus

	// Every node gets an entry, so nodes running no selected pods show as 0.
	perNode := make(map[string]int32, len(nodes))
	for i := range nodes {
		status.NodeCount++
		if IsNodeReady(&nodes[i]) {
			status.ReadyNodes++
		}
		perNode[nodes[i].Name] = 0
	}
	status.NotReadyNodes = status.NodeCount - status.ReadyNodes

	for i := range pods {
		pod := &pods[i]
		if !InNamespaces(pod.Namespace, namespaces) {
			continue
		}
		status.PodCount++
		addPhase(&status.PodPhases, pod.Status.Phase)
		if pod.Spec.NodeName != "" {
			perNode[pod.Spec.NodeName]++
		}
	}

	for name, count := range perNode {
		status.PodsPerNode = append(status.PodsPerNode, observabilityv1alpha1.NodePodCount{NodeName: name, Pods: count})
	}
	// Map iteration order is random; sort so an unchanged cluster yields an
	// identical status and the reconciler can skip a no-op update.
	slices.SortFunc(status.PodsPerNode, func(a, b observabilityv1alpha1.NodePodCount) int {
		return cmp.Compare(a.NodeName, b.NodeName)
	})

	return status
}

// InNamespaces reports whether a pod in namespace passes a ClusterView's
// namespace filter. An empty filter selects every namespace.
func InNamespaces(namespace string, namespaces []string) bool {
	return len(namespaces) == 0 || slices.Contains(namespaces, namespace)
}

// IsNodeReady reports whether the node's Ready condition is True. A missing
// condition counts as not ready.
func IsNodeReady(node *corev1.Node) bool {
	for _, c := range node.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func addPhase(counts *observabilityv1alpha1.PodPhaseCounts, phase corev1.PodPhase) {
	switch phase {
	case corev1.PodPending:
		counts.Pending++
	case corev1.PodRunning:
		counts.Running++
	case corev1.PodSucceeded:
		counts.Succeeded++
	case corev1.PodFailed:
		counts.Failed++
	default:
		counts.Unknown++
	}
}
