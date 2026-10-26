package apiserver

import (
	"cmp"
	"net/http"
	"slices"

	corev1 "k8s.io/api/core/v1"

	"github.com/arjunmalhotra1/aranya/internal/summary"
)

// Node is one entry in the GET /api/nodes response.
type Node struct {
	Name        string          `json:"name"`
	Ready       bool            `json:"ready"`
	Conditions  []NodeCondition `json:"conditions"`
	Capacity    Resources       `json:"capacity"`
	Allocatable Resources       `json:"allocatable"`
	PodCount    int             `json:"podCount"`
}

// NodeCondition is a node condition such as Ready or MemoryPressure.
type NodeCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// Resources holds CPU and memory as plain numbers so the UI can compare them
// without parsing Kubernetes quantities like "3900m" or "8Gi".
type Resources struct {
	CPUMillis   int64 `json:"cpuMillis"`
	MemoryBytes int64 `json:"memoryBytes"`
}

func (s *Server) listNodes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var nodes corev1.NodeList
	if err := s.reader.List(ctx, &nodes); err != nil {
		writeError(ctx, w, http.StatusInternalServerError, "list nodes", err)
		return
	}
	var pods corev1.PodList
	if err := s.reader.List(ctx, &pods); err != nil {
		writeError(ctx, w, http.StatusInternalServerError, "list pods", err)
		return
	}

	podsPerNode := make(map[string]int, len(nodes.Items))
	for i := range pods.Items {
		if name := pods.Items[i].Spec.NodeName; name != "" {
			podsPerNode[name]++
		}
	}

	out := make([]Node, 0, len(nodes.Items))
	for i := range nodes.Items {
		out = append(out, toNode(&nodes.Items[i], podsPerNode[nodes.Items[i].Name]))
	}
	slices.SortFunc(out, func(a, b Node) int { return cmp.Compare(a.Name, b.Name) })

	writeJSON(ctx, w, out)
}

func toNode(node *corev1.Node, podCount int) Node {
	conditions := make([]NodeCondition, 0, len(node.Status.Conditions))
	for _, c := range node.Status.Conditions {
		conditions = append(conditions, NodeCondition{
			Type:    string(c.Type),
			Status:  string(c.Status),
			Reason:  c.Reason,
			Message: c.Message,
		})
	}
	return Node{
		Name:        node.Name,
		Ready:       summary.IsNodeReady(node),
		Conditions:  conditions,
		Capacity:    toResources(node.Status.Capacity),
		Allocatable: toResources(node.Status.Allocatable),
		PodCount:    podCount,
	}
}

func toResources(list corev1.ResourceList) Resources {
	return Resources{
		CPUMillis:   list.Cpu().MilliValue(),
		MemoryBytes: list.Memory().Value(),
	}
}
