package apiserver

import (
	"cmp"
	"net/http"
	"slices"

	"k8s.io/apimachinery/pkg/api/meta"

	observabilityv1alpha1 "github.com/arjunmalhotra1/aranya/api/v1alpha1"
)

// ClusterView is one entry in the GET /api/clusterviews response: a view's
// namespace filter plus the summary its reconciler last wrote.
type ClusterView struct {
	Name string `json:"name"`
	// Namespaces is the pod filter; empty means all namespaces.
	Namespaces []string `json:"namespaces"`
	Ready      bool     `json:"ready"`
	Message    string   `json:"message,omitempty"`
	NodeCount  int32    `json:"nodeCount"`
	ReadyNodes int32    `json:"readyNodes"`
	PodCount   int32    `json:"podCount"`
}

func (s *Server) listClusterViews(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var views observabilityv1alpha1.ClusterViewList
	if err := s.reader.List(ctx, &views); err != nil {
		writeError(ctx, w, http.StatusInternalServerError, "list clusterviews", err)
		return
	}

	out := make([]ClusterView, 0, len(views.Items))
	for i := range views.Items {
		out = append(out, toClusterView(&views.Items[i]))
	}
	slices.SortFunc(out, func(a, b ClusterView) int { return cmp.Compare(a.Name, b.Name) })

	writeJSON(ctx, w, out)
}

func toClusterView(cv *observabilityv1alpha1.ClusterView) ClusterView {
	out := ClusterView{
		Name:       cv.Name,
		Namespaces: cv.Spec.Namespaces,
		NodeCount:  cv.Status.NodeCount,
		ReadyNodes: cv.Status.ReadyNodes,
		PodCount:   cv.Status.PodCount,
	}
	if out.Namespaces == nil {
		out.Namespaces = []string{} // encode as [] rather than null
	}
	if ready := meta.FindStatusCondition(cv.Status.Conditions, observabilityv1alpha1.ConditionReady); ready != nil {
		out.Ready = ready.Status == "True"
		out.Message = ready.Message
	}
	return out
}
