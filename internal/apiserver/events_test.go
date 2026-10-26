package apiserver

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var baseTime = time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)

func testEvent(namespace, name, kind, object, eventType, reason string, minutes int) *corev1.Event {
	return &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Namespace: namespace, Name: name},
		InvolvedObject: corev1.ObjectReference{Kind: kind, Name: object, Namespace: namespace},
		Type:           eventType,
		Reason:         reason,
		Message:        reason + " message",
		Count:          1,
		LastTimestamp:  metav1.NewTime(baseTime.Add(time.Duration(minutes) * time.Minute)),
	}
}

func TestListNodeEvents(t *testing.T) {
	s := newServer(
		testEvent(defaultNS, "e1", "Node", "node-a", corev1.EventTypeNormal, "Starting", 1),
		testEvent(defaultNS, "e2", "Node", "node-a", corev1.EventTypeWarning, "NodeNotReady", 5),
		testEvent(defaultNS, "e3", "Node", "node-b", corev1.EventTypeNormal, "Starting", 2),
		testEvent(defaultNS, "e4", "Pod", "node-a", corev1.EventTypeNormal, "Pulled", 9), // same name, different kind
	)

	rec := get(t, s, http.MethodGet, "/api/nodes/node-a/events")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := decode[[]Event](t, rec)
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2 (only node-a's Node events): %+v", len(got), got)
	}
	if got[0].Reason != "NodeNotReady" || got[1].Reason != "Starting" {
		t.Errorf("order = %s, %s; want newest first (NodeNotReady, Starting)", got[0].Reason, got[1].Reason)
	}
	if got[0].Type != corev1.EventTypeWarning || got[0].Count != 1 || !got[0].LastSeen.Equal(baseTime.Add(5*time.Minute)) {
		t.Errorf("first event = %+v", got[0])
	}
}

func TestListPodEventsIsScopedToNamespace(t *testing.T) {
	s := newServer(
		testEvent(defaultNS, "e1", "Pod", "web", corev1.EventTypeWarning, "BackOff", 3),
		testEvent("other", "e2", "Pod", "web", corev1.EventTypeNormal, "Pulled", 4),
	)

	rec := get(t, s, http.MethodGet, "/api/namespaces/default/pods/web/events")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := decode[[]Event](t, rec)
	if len(got) != 1 || got[0].Reason != "BackOff" {
		t.Errorf("events = %+v, want only default/web's BackOff", got)
	}
}

func TestListEventsCapsAtNewest(t *testing.T) {
	objs := make([]client.Object, 0, maxEvents+5)
	for i := range maxEvents + 5 {
		objs = append(objs, testEvent(defaultNS, fmt.Sprintf("e%d", i), "Node", "node-a", corev1.EventTypeNormal, fmt.Sprintf("R%d", i), i))
	}
	got := decode[[]Event](t, get(t, newServer(objs...), http.MethodGet, "/api/nodes/node-a/events"))
	if len(got) != maxEvents {
		t.Fatalf("got %d events, want %d", len(got), maxEvents)
	}
	if want := fmt.Sprintf("R%d", maxEvents+4); got[0].Reason != want {
		t.Errorf("first = %s, want the newest %s", got[0].Reason, want)
	}
}

func TestListEventsNoneReturnsEmptyArray(t *testing.T) {
	rec := get(t, newServer(), http.MethodGet, "/api/nodes/node-a/events")
	if body := strings.TrimSpace(rec.Body.String()); rec.Code != http.StatusOK || body != "[]" {
		t.Errorf("got %d %s, want 200 []", rec.Code, body)
	}
}

func TestListEventsFailureReturns500WithoutLeaking(t *testing.T) {
	rec := get(t, failingServer(), http.MethodGet, "/api/nodes/node-a/events")
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "secret") {
		t.Errorf("got %d %s, want 500 without the internal error", rec.Code, rec.Body.String())
	}
}
