package apiserver

import (
	"cmp"
	"net/http"
	"slices"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Pod is one entry in the GET /api/nodes/{name}/pods response.
type Pod struct {
	Namespace       string `json:"namespace"`
	Name            string `json:"name"`
	Phase           string `json:"phase"`
	Restarts        int32  `json:"restarts"`
	ReadyContainers int    `json:"readyContainers"`
	TotalContainers int    `json:"totalContainers"`
}

func (s *Server) listNodePods(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nodeName := r.PathValue("name")

	// Check the node exists so a typo gets 404 instead of an empty list.
	var node corev1.Node
	if err := s.reader.Get(ctx, client.ObjectKey{Name: nodeName}, &node); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(ctx, w, http.StatusNotFound, "node not found", err)
			return
		}
		writeError(ctx, w, http.StatusInternalServerError, "get node", err)
		return
	}

	var pods corev1.PodList
	if err := s.reader.List(ctx, &pods); err != nil {
		writeError(ctx, w, http.StatusInternalServerError, "list pods", err)
		return
	}

	out := []Pod{}
	for i := range pods.Items {
		if pods.Items[i].Spec.NodeName == nodeName {
			out = append(out, toPod(&pods.Items[i]))
		}
	}
	slices.SortFunc(out, func(a, b Pod) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
	})

	writeJSON(ctx, w, out)
}

// toPod sums restarts and counts ready containers across the pod's regular
// containers, matching the RESTARTS and READY columns of kubectl get pods.
func toPod(pod *corev1.Pod) Pod {
	out := Pod{
		Namespace:       pod.Namespace,
		Name:            pod.Name,
		Phase:           string(pod.Status.Phase),
		TotalContainers: len(pod.Spec.Containers),
	}
	for _, cs := range pod.Status.ContainerStatuses {
		out.Restarts += cs.RestartCount
		if cs.Ready {
			out.ReadyContainers++
		}
	}
	return out
}
