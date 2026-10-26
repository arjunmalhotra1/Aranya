package apiserver

import (
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	observabilityv1alpha1 "github.com/arjunmalhotra1/aranya/api/v1alpha1"
)

// viewNamespaces returns the namespace filter of the ClusterView named by the
// ?view= query parameter; no parameter means no filter. On failure it writes
// the error response (404 for an unknown view) and returns ok=false.
func (s *Server) viewNamespaces(w http.ResponseWriter, r *http.Request) (namespaces []string, ok bool) {
	name := r.URL.Query().Get("view")
	if name == "" {
		return nil, true
	}

	ctx := r.Context()
	var cv observabilityv1alpha1.ClusterView
	if err := s.reader.Get(ctx, client.ObjectKey{Name: name}, &cv); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(ctx, w, http.StatusNotFound, "clusterview not found", err)
			return nil, false
		}
		writeError(ctx, w, http.StatusInternalServerError, "get clusterview", err)
		return nil, false
	}
	return cv.Spec.Namespaces, true
}
