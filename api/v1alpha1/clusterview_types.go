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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// ClusterViewSpec configures what the operator observes.
type ClusterViewSpec struct {
	// namespaces limits which pods are counted. Empty means all namespaces.
	// Nodes are cluster-scoped, so they are always included.
	// +optional
	// +listType=set
	// +kubebuilder:validation:items:MaxLength=63
	// +kubebuilder:validation:items:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Namespaces []string `json:"namespaces,omitempty"`

	// refreshInterval is how often the summary is recomputed even when no
	// watch event arrives. Watches remain the primary trigger.
	// +optional
	// +kubebuilder:default="30s"
	// +kubebuilder:validation:XValidation:rule="duration(self) >= duration('5s')",message="refreshInterval must be at least 5s"
	RefreshInterval *metav1.Duration `json:"refreshInterval,omitempty"`
}

// ClusterViewStatus defines the observed state of ClusterView.
type ClusterViewStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// nodeCount is the total number of nodes in the cluster.
	// +optional
	// +kubebuilder:validation:Minimum=0
	NodeCount int32 `json:"nodeCount,omitempty"`

	// readyNodes is the number of nodes whose Ready condition is True.
	// +optional
	// +kubebuilder:validation:Minimum=0
	ReadyNodes int32 `json:"readyNodes,omitempty"`

	// notReadyNodes is the number of nodes whose Ready condition is False or Unknown.
	// +optional
	// +kubebuilder:validation:Minimum=0
	NotReadyNodes int32 `json:"notReadyNodes,omitempty"`

	// podCount is the number of pods in the namespaces selected by spec.namespaces.
	// +optional
	// +kubebuilder:validation:Minimum=0
	PodCount int32 `json:"podCount,omitempty"`

	// podPhases counts the selected pods by phase.
	// +optional
	PodPhases PodPhaseCounts `json:"podPhases,omitzero"`

	// podsPerNode counts the selected pods scheduled on each node.
	// Pods not yet scheduled to a node are not listed.
	// +optional
	// +listType=map
	// +listMapKey=nodeName
	PodsPerNode []NodePodCount `json:"podsPerNode,omitempty"`

	// observedGeneration is the spec generation this status was computed from.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// lastUpdated is when this status was last computed.
	// +optional
	LastUpdated *metav1.Time `json:"lastUpdated,omitempty"`

	// conditions represent the current state of the ClusterView resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// PodPhaseCounts holds the number of pods in each pod phase.
type PodPhaseCounts struct {
	// +optional
	Pending int32 `json:"pending,omitempty"`
	// +optional
	Running int32 `json:"running,omitempty"`
	// +optional
	Succeeded int32 `json:"succeeded,omitempty"`
	// +optional
	Failed int32 `json:"failed,omitempty"`
	// +optional
	Unknown int32 `json:"unknown,omitempty"`
}

// NodePodCount is the number of selected pods scheduled on one node.
type NodePodCount struct {
	// nodeName is the name of the node.
	// +required
	NodeName string `json:"nodeName"`

	// pods is the number of selected pods on the node.
	// +required
	// +kubebuilder:validation:Minimum=0
	Pods int32 `json:"pods"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Nodes",type=integer,JSONPath=`.status.nodeCount`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.readyNodes`
// +kubebuilder:printcolumn:name="Pods",type=integer,JSONPath=`.status.podCount`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ClusterView is the Schema for the clusterviews API
type ClusterView struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of ClusterView
	// +required
	Spec ClusterViewSpec `json:"spec"`

	// status defines the observed state of ClusterView
	// +optional
	Status ClusterViewStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ClusterViewList contains a list of ClusterView
type ClusterViewList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ClusterView `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &ClusterView{}, &ClusterViewList{})
		return nil
	})
}
