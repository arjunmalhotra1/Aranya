// Package apiserver serves the cluster state over HTTP for the web UI. It reads
// from the manager's informer cache, so requests never hit the Kubernetes API
// server directly.
package apiserver

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// The API reads Events on demand with an uncached, field-filtered list, so it
// needs neither watch nor get.
// +kubebuilder:rbac:groups="",resources=events,verbs=list

const shutdownTimeout = 5 * time.Second

// Server is an HTTP server that runs inside the controller manager.
type Server struct {
	addr   string
	reader client.Reader
	events client.Reader
	mux    *http.ServeMux
}

// New returns a Server listening on addr. reader is normally the manager's
// cache-backed client; events is the manager's uncached API reader, used only
// for Events, which are not worth caching.
func New(addr string, reader, events client.Reader) *Server {
	s := &Server{addr: addr, reader: reader, events: events, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /api/nodes", s.listNodes)
	s.mux.HandleFunc("GET /api/nodes/{name}/pods", s.listNodePods)
	s.mux.HandleFunc("GET /api/nodes/{name}/events", s.listNodeEvents)
	s.mux.HandleFunc("GET /api/namespaces/{namespace}/pods/{pod}/events", s.listPodEvents)
	s.mux.HandleFunc("GET /api/clusterviews", s.listClusterViews)
	// Everything else is the web UI. The more specific /api/ patterns win.
	s.mux.Handle("GET /", http.FileServerFS(uiFiles()))
	return s
}

// staticFiles holds the web UI, compiled into the binary by go build.
//
//go:embed static
var staticFiles embed.FS

func uiFiles() fs.FS {
	ui, err := fs.Sub(staticFiles, "static")
	if err != nil {
		// Only fails if the embedded directory is missing, which the
		// go:embed line above rules out at compile time.
		panic(err)
	}
	return ui
}

// Start implements manager.Runnable. It serves until ctx is cancelled, then
// shuts down gracefully, letting in-flight requests finish.
func (s *Server) Start(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("apiserver")
	srv := &http.Server{
		Addr:              s.addr,
		Handler:           s.mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		log.Info("serving", "addr", s.addr)
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		return fmt.Errorf("serve %s: %w", s.addr, err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		log.Info("shutting down")
		if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	}
}

// NeedLeaderElection implements manager.LeaderElectionRunnable. The API is
// read-only, so every replica serves it, not only the leader.
func (s *Server) NeedLeaderElection() bool {
	return false
}
