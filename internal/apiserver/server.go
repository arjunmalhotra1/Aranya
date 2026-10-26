// Package apiserver serves the cluster state over HTTP for the web UI. It reads
// from the manager's informer cache, so requests never hit the Kubernetes API
// server directly.
package apiserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const shutdownTimeout = 5 * time.Second

// Server is an HTTP server that runs inside the controller manager.
type Server struct {
	addr   string
	reader client.Reader
	mux    *http.ServeMux
}

// New returns a Server listening on addr and reading cluster state from reader,
// normally the manager's cache-backed client.
func New(addr string, reader client.Reader) *Server {
	s := &Server{addr: addr, reader: reader, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /api/nodes", s.listNodes)
	s.mux.HandleFunc("GET /api/nodes/{name}/pods", s.listNodePods)
	return s
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
