package apiserver

import (
	"context"
	"encoding/json"
	"net/http"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

func writeJSON(ctx context.Context, w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// Headers are already sent, so the client cannot be told; just log it.
		logf.FromContext(ctx).Error(err, "write response")
	}
}

// writeError logs the full error and sends the client only a short message,
// so internal details such as API-server errors are not exposed.
func writeError(ctx context.Context, w http.ResponseWriter, status int, msg string, err error) {
	logf.FromContext(ctx).Error(err, msg)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
