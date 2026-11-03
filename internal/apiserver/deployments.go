package apiserver

import (
	"cmp"
	"net/http"
	"slices"

	appsv1 "k8s.io/api/apps/v1"

	"github.com/arjunmalhotra1/aranya/internal/summary"
)

// Deployment is one entry in the GET /api/deployments response. Desired,
// Ready, UpToDate and Available match the columns of kubectl get deployments.
type Deployment struct {
	Namespace string   `json:"namespace"`
	Name      string   `json:"name"`
	Desired   int32    `json:"desired"`
	Ready     int32    `json:"ready"`
	UpToDate  int32    `json:"upToDate"`
	Available int32    `json:"available"`
	RolledOut bool     `json:"rolledOut"`
	Images    []string `json:"images"`
}

func (s *Server) listDeployments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespaces, ok := s.viewNamespaces(w, r)
	if !ok {
		return
	}

	var deployments appsv1.DeploymentList
	if err := s.reader.List(ctx, &deployments); err != nil {
		writeError(ctx, w, http.StatusInternalServerError, "list deployments", err)
		return
	}

	out := make([]Deployment, 0, len(deployments.Items))
	for i := range deployments.Items {
		if summary.InNamespaces(deployments.Items[i].Namespace, namespaces) {
			out = append(out, toDeployment(&deployments.Items[i]))
		}
	}
	slices.SortFunc(out, func(a, b Deployment) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
	})

	writeJSON(ctx, w, out)
}

// toDeployment uses summary.IsDeploymentAvailable for RolledOut, so the UI and
// the ClusterView's status.deployments always agree.
func toDeployment(d *appsv1.Deployment) Deployment {
	desired := int32(1)
	if d.Spec.Replicas != nil {
		desired = *d.Spec.Replicas
	}
	images := make([]string, 0, len(d.Spec.Template.Spec.Containers))
	for _, c := range d.Spec.Template.Spec.Containers {
		images = append(images, c.Image)
	}
	return Deployment{
		Namespace: d.Namespace,
		Name:      d.Name,
		Desired:   desired,
		Ready:     d.Status.ReadyReplicas,
		UpToDate:  d.Status.UpdatedReplicas,
		Available: d.Status.AvailableReplicas,
		RolledOut: summary.IsDeploymentAvailable(d),
		Images:    images,
	}
}
