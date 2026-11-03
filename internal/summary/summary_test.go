package summary

import (
	"reflect"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	observabilityv1alpha1 "github.com/arjunmalhotra1/aranya/api/v1alpha1"
)

const (
	defaultNS = "default"
	systemNS  = "kube-system"
)

func node(name string, ready corev1.ConditionStatus) corev1.Node {
	n := corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if ready != "" {
		n.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: ready}}
	}
	return n
}

func pod(namespace, nodeName string, phase corev1.PodPhase) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace},
		Spec:       corev1.PodSpec{NodeName: nodeName},
		Status:     corev1.PodStatus{Phase: phase},
	}
}

func TestCompute(t *testing.T) {
	tests := []struct {
		name       string
		nodes      []corev1.Node
		pods       []corev1.Pod
		namespaces []string
		want       observabilityv1alpha1.ClusterViewStatus
	}{
		{
			name: "empty cluster",
			want: observabilityv1alpha1.ClusterViewStatus{},
		},
		{
			name: "node readiness: True is ready; False, Unknown and missing are not",
			nodes: []corev1.Node{
				node("a", corev1.ConditionTrue),
				node("b", corev1.ConditionFalse),
				node("c", corev1.ConditionUnknown),
				node("d", ""),
			},
			want: observabilityv1alpha1.ClusterViewStatus{
				NodeCount:     4,
				ReadyNodes:    1,
				NotReadyNodes: 3,
				PodsPerNode: []observabilityv1alpha1.NodePodCount{
					{NodeName: "a"}, {NodeName: "b"}, {NodeName: "c"}, {NodeName: "d"},
				},
			},
		},
		{
			name:  "pods counted by phase and node, unscheduled pods not placed on a node",
			nodes: []corev1.Node{node("b", corev1.ConditionTrue), node("a", corev1.ConditionTrue)},
			pods: []corev1.Pod{
				pod(defaultNS, "a", corev1.PodRunning),
				pod(defaultNS, "a", corev1.PodRunning),
				pod(defaultNS, "b", corev1.PodSucceeded),
				pod(defaultNS, "b", corev1.PodFailed),
				pod(defaultNS, "", corev1.PodPending),
				pod(defaultNS, "b", ""),
			},
			want: observabilityv1alpha1.ClusterViewStatus{
				NodeCount:  2,
				ReadyNodes: 2,
				PodCount:   6,
				PodPhases: observabilityv1alpha1.PodPhaseCounts{
					Pending: 1, Running: 2, Succeeded: 1, Failed: 1, Unknown: 1,
				},
				PodsPerNode: []observabilityv1alpha1.NodePodCount{
					{NodeName: "a", Pods: 2}, {NodeName: "b", Pods: 3},
				},
			},
		},
		{
			name:       "namespace filter skips pods outside the list",
			nodes:      []corev1.Node{node("a", corev1.ConditionTrue)},
			namespaces: []string{defaultNS},
			pods: []corev1.Pod{
				pod(defaultNS, "a", corev1.PodRunning),
				pod(systemNS, "a", corev1.PodRunning),
			},
			want: observabilityv1alpha1.ClusterViewStatus{
				NodeCount:   1,
				ReadyNodes:  1,
				PodCount:    1,
				PodPhases:   observabilityv1alpha1.PodPhaseCounts{Running: 1},
				PodsPerNode: []observabilityv1alpha1.NodePodCount{{NodeName: "a", Pods: 1}},
			},
		},
		{
			name: "pod on a node missing from the node list is still counted",
			pods: []corev1.Pod{pod(defaultNS, "gone", corev1.PodRunning)},
			want: observabilityv1alpha1.ClusterViewStatus{
				PodCount:    1,
				PodPhases:   observabilityv1alpha1.PodPhaseCounts{Running: 1},
				PodsPerNode: []observabilityv1alpha1.NodePodCount{{NodeName: "gone", Pods: 1}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Compute(tt.nodes, tt.pods, tt.namespaces)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Compute() =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}

// deployment returns a Deployment wanting desired replicas, with the given
// updated and available counts; replicas is the total, including old ones.
func deployment(namespace string, desired, replicas, updated, available int32) appsv1.Deployment {
	return appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Generation: 2},
		Spec:       appsv1.DeploymentSpec{Replicas: &desired},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 2,
			Replicas:           replicas,
			UpdatedReplicas:    updated,
			AvailableReplicas:  available,
		},
	}
}

func TestIsDeploymentAvailable(t *testing.T) {
	stale := deployment(defaultNS, 3, 3, 3, 3)
	stale.Generation = 3 // spec edited, controller has not caught up
	noReplicas := deployment(defaultNS, 1, 1, 1, 1)
	noReplicas.Spec.Replicas = nil // defaults to 1

	tests := []struct {
		name string
		d    appsv1.Deployment
		want bool
	}{
		{"fully rolled out", deployment(defaultNS, 3, 3, 3, 3), true},
		{"scaled to zero", deployment(defaultNS, 0, 0, 0, 0), true},
		{"nil replicas means one", noReplicas, true},
		{"spec not yet observed", stale, false},
		{"mid-rollout: old replicas still running", deployment(defaultNS, 3, 4, 2, 3), false},
		{"updated but not yet available", deployment(defaultNS, 3, 3, 3, 2), false},
		{"no pods available", deployment(defaultNS, 2, 2, 2, 0), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsDeploymentAvailable(&tt.d); got != tt.want {
				t.Errorf("IsDeploymentAvailable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCountDeployments(t *testing.T) {
	deployments := []appsv1.Deployment{
		deployment(defaultNS, 3, 3, 3, 3),
		deployment(defaultNS, 3, 3, 3, 1),
		deployment(systemNS, 1, 1, 1, 1),
	}
	tests := []struct {
		name       string
		namespaces []string
		want       observabilityv1alpha1.DeploymentCounts
	}{
		{"empty filter selects all", nil, observabilityv1alpha1.DeploymentCounts{Total: 3, Available: 2, Unavailable: 1}},
		{"filter skips other namespaces", []string{defaultNS}, observabilityv1alpha1.DeploymentCounts{Total: 2, Available: 1, Unavailable: 1}},
		{"no matches", []string{"other"}, observabilityv1alpha1.DeploymentCounts{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CountDeployments(deployments, tt.namespaces); got != tt.want {
				t.Errorf("CountDeployments() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestInNamespaces(t *testing.T) {
	tests := []struct {
		namespace  string
		namespaces []string
		want       bool
	}{
		{defaultNS, nil, true},
		{defaultNS, []string{}, true},
		{defaultNS, []string{defaultNS}, true},
		{systemNS, []string{defaultNS}, false},
		{systemNS, []string{defaultNS, systemNS}, true},
	}
	for _, tt := range tests {
		if got := InNamespaces(tt.namespace, tt.namespaces); got != tt.want {
			t.Errorf("InNamespaces(%q, %v) = %v, want %v", tt.namespace, tt.namespaces, got, tt.want)
		}
	}
}
