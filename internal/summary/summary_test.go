package summary

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	observabilityv1alpha1 "github.com/arjunmalhotra1/aranya/api/v1alpha1"
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
				pod("default", "a", corev1.PodRunning),
				pod("default", "a", corev1.PodRunning),
				pod("default", "b", corev1.PodSucceeded),
				pod("default", "b", corev1.PodFailed),
				pod("default", "", corev1.PodPending),
				pod("default", "b", ""),
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
			namespaces: []string{"default"},
			pods: []corev1.Pod{
				pod("default", "a", corev1.PodRunning),
				pod("kube-system", "a", corev1.PodRunning),
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
			pods: []corev1.Pod{pod("default", "gone", corev1.PodRunning)},
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
