package apiserver

import (
	"net/http"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// maxEvents caps the response; the newest events are kept.
	maxEvents = 20

	// Event fields the API server can filter on, so only the clicked object's
	// events are returned.
	involvedKindField = "involvedObject.kind"
	involvedNameField = "involvedObject.name"
)

// Event is one entry in an events response.
type Event struct {
	Type     string    `json:"type"`
	Reason   string    `json:"reason"`
	Message  string    `json:"message"`
	Count    int32     `json:"count"`
	LastSeen time.Time `json:"lastSeen"`
}

func (s *Server) listNodeEvents(w http.ResponseWriter, r *http.Request) {
	// Node events live in whichever namespace the reporter chose (usually
	// default), so search all namespaces.
	s.writeEvents(w, r, "Node", r.PathValue("name"))
}

func (s *Server) listPodEvents(w http.ResponseWriter, r *http.Request) {
	s.writeEvents(w, r, "Pod", r.PathValue("pod"), client.InNamespace(r.PathValue("namespace")))
}

// writeEvents lists the events about one object, newest first. Events are
// numerous and short-lived, so they are read on demand through the uncached
// reader instead of being watched into the cache.
func (s *Server) writeEvents(w http.ResponseWriter, r *http.Request, kind, name string, opts ...client.ListOption) {
	ctx := r.Context()
	opts = append(opts, client.MatchingFields{involvedKindField: kind, involvedNameField: name})
	var events corev1.EventList
	if err := s.events.List(ctx, &events, opts...); err != nil {
		writeError(ctx, w, http.StatusInternalServerError, "list events", err)
		return
	}

	out := make([]Event, 0, len(events.Items))
	for i := range events.Items {
		out = append(out, toEvent(&events.Items[i]))
	}
	slices.SortFunc(out, func(a, b Event) int { return b.LastSeen.Compare(a.LastSeen) })
	if len(out) > maxEvents {
		out = out[:maxEvents]
	}

	writeJSON(ctx, w, out)
}

func toEvent(e *corev1.Event) Event {
	count := e.Count
	if count == 0 && e.Series != nil {
		count = e.Series.Count
	}
	return Event{
		Type:     e.Type,
		Reason:   e.Reason,
		Message:  e.Message,
		Count:    max(count, 1),
		LastSeen: lastSeen(e),
	}
}

// lastSeen picks the most specific timestamp set on the event; which ones are
// populated depends on the component that recorded it.
func lastSeen(e *corev1.Event) time.Time {
	for _, t := range []time.Time{
		e.LastTimestamp.Time,
		e.EventTime.Time,
		e.FirstTimestamp.Time,
		e.CreationTimestamp.Time,
	} {
		if !t.IsZero() {
			return t
		}
	}
	return time.Time{}
}
