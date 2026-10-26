package apiserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	observabilityv1alpha1 "github.com/arjunmalhotra1/aranya/api/v1alpha1"
)

const (
	kubeletReady = "KubeletReady"
	defaultNS    = "default"
	allView      = "all"
)

func testNode(name string, ready corev1.ConditionStatus) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: ready, Reason: kubeletReady}},
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("1900m"), corev1.ResourceMemory: resource.MustParse("3Gi"),
			},
		},
	}
}

func testPod(namespace, name, nodeName string, restarts int32, ready bool) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec:       corev1.PodSpec{NodeName: nodeName, Containers: []corev1.Container{{Name: "app"}}},
		Status: corev1.PodStatus{
			Phase:             corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{Name: "app", RestartCount: restarts, Ready: ready}},
		},
	}
}

// get sends a request through the server's routes and returns the response.
func get(t *testing.T, s *Server, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return out
}

// fakeClient builds a fake client that knows the ClusterView type and has the
// same pod index the manager's cache has, so MatchingFields queries work.
func fakeClient(objs ...client.Object) *fake.ClientBuilder {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(observabilityv1alpha1.AddToScheme(scheme))
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithIndex(&corev1.Pod{}, PodNodeNameField, podNodeName).
		WithIndex(&corev1.Event{}, involvedKindField, func(o client.Object) []string { return []string{o.(*corev1.Event).InvolvedObject.Kind} }).
		WithIndex(&corev1.Event{}, involvedNameField, func(o client.Object) []string { return []string{o.(*corev1.Event).InvolvedObject.Name} })
}

func newServer(objs ...client.Object) *Server {
	c := fakeClient(objs...).Build()
	return New(":0", c, c)
}

// failingServer returns a server whose List calls fail, as if the cache or
// API server were unavailable.
func failingServer(objs ...client.Object) *Server {
	c := fakeClient(objs...).WithInterceptorFuncs(interceptor.Funcs{
		List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
			return errors.New("secret internal detail")
		},
	}).Build()
	return New(":0", c, c)
}

func TestListNodes(t *testing.T) {
	t.Run("returns nodes sorted by name with readiness, resources and pod counts", func(t *testing.T) {
		s := newServer(
			testNode("node-b", corev1.ConditionFalse),
			testNode("node-a", corev1.ConditionTrue),
			testPod(defaultNS, "web", "node-a", 0, true),
			testPod(defaultNS, "db", "node-a", 0, true),
			testPod(defaultNS, "pending", "", 0, false),
		)
		rec := get(t, s, http.MethodGet, "/api/nodes")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}

		resources := func(cpu, mem int64) Resources { return Resources{CPUMillis: cpu, MemoryBytes: mem} }
		want := []Node{
			{
				Name: "node-a", Ready: true,
				Conditions:  []NodeCondition{{Type: "Ready", Status: "True", Reason: kubeletReady}},
				Capacity:    resources(2000, 4<<30),
				Allocatable: resources(1900, 3<<30),
				PodCount:    2,
			},
			{
				Name: "node-b", Ready: false,
				Conditions:  []NodeCondition{{Type: "Ready", Status: "False", Reason: kubeletReady}},
				Capacity:    resources(2000, 4<<30),
				Allocatable: resources(1900, 3<<30),
				PodCount:    0,
			},
		}
		if got := decode[[]Node](t, rec); !reflect.DeepEqual(got, want) {
			t.Errorf("body =\n%+v\nwant\n%+v", got, want)
		}
	})

	t.Run("empty cluster returns an empty JSON array", func(t *testing.T) {
		rec := get(t, newServer(), http.MethodGet, "/api/nodes")
		if body := strings.TrimSpace(rec.Body.String()); rec.Code != http.StatusOK || body != "[]" {
			t.Errorf("got %d %s, want 200 []", rec.Code, body)
		}
	})

	t.Run("non-GET method is rejected", func(t *testing.T) {
		if rec := get(t, newServer(), http.MethodPost, "/api/nodes"); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want 405", rec.Code)
		}
	})

	t.Run("list failure returns 500 without leaking the error", func(t *testing.T) {
		rec := get(t, failingServer(), http.MethodGet, "/api/nodes")
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
		if strings.Contains(rec.Body.String(), "secret") {
			t.Errorf("body leaks internal error: %s", rec.Body.String())
		}
	})
}

func TestListNodePods(t *testing.T) {
	t.Run("returns only that node's pods, sorted, with restarts and readiness", func(t *testing.T) {
		s := newServer(
			testNode("node-a", corev1.ConditionTrue),
			testPod("kube-system", "dns", "node-a", 0, true),
			testPod(defaultNS, "web", "node-a", 3, false),
			testPod(defaultNS, "other", "node-b", 0, true),
		)
		rec := get(t, s, http.MethodGet, "/api/nodes/node-a/pods")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		want := []Pod{
			{Namespace: defaultNS, Name: "web", Phase: "Running", Restarts: 3, ReadyContainers: 0, TotalContainers: 1},
			{Namespace: "kube-system", Name: "dns", Phase: "Running", Restarts: 0, ReadyContainers: 1, TotalContainers: 1},
		}
		if got := decode[[]Pod](t, rec); !reflect.DeepEqual(got, want) {
			t.Errorf("body =\n%+v\nwant\n%+v", got, want)
		}
	})

	t.Run("node with no pods returns an empty JSON array", func(t *testing.T) {
		rec := get(t, newServer(testNode("node-a", corev1.ConditionTrue)), http.MethodGet, "/api/nodes/node-a/pods")
		if body := strings.TrimSpace(rec.Body.String()); rec.Code != http.StatusOK || body != "[]" {
			t.Errorf("got %d %s, want 200 []", rec.Code, body)
		}
	})

	t.Run("unknown node returns 404", func(t *testing.T) {
		rec := get(t, newServer(), http.MethodGet, "/api/nodes/nope/pods")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
		if got := decode[map[string]string](t, rec); got["error"] != "node not found" {
			t.Errorf("error = %q, want %q", got["error"], "node not found")
		}
	})

	t.Run("list failure returns 500 without leaking the error", func(t *testing.T) {
		rec := get(t, failingServer(testNode("node-a", corev1.ConditionTrue)), http.MethodGet, "/api/nodes/node-a/pods")
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
		if strings.Contains(rec.Body.String(), "secret") {
			t.Errorf("body leaks internal error: %s", rec.Body.String())
		}
	})
}

func TestListClusterViews(t *testing.T) {
	t.Run("returns views sorted by name with filter and status summary", func(t *testing.T) {
		filtered := &observabilityv1alpha1.ClusterView{
			ObjectMeta: metav1.ObjectMeta{Name: "team-a"},
			Spec:       observabilityv1alpha1.ClusterViewSpec{Namespaces: []string{defaultNS}},
			Status: observabilityv1alpha1.ClusterViewStatus{
				NodeCount: 3, ReadyNodes: 3, PodCount: 2,
				Conditions: []metav1.Condition{{
					Type: observabilityv1alpha1.ConditionReady, Status: metav1.ConditionTrue,
					Reason: "StatusComputed", Message: "Counted 3 nodes and 2 pods",
				}},
			},
		}
		all := &observabilityv1alpha1.ClusterView{ObjectMeta: metav1.ObjectMeta{Name: allView}}

		rec := get(t, newServer(filtered, all), http.MethodGet, "/api/clusterviews")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		want := []ClusterView{
			{Name: allView, Namespaces: []string{}},
			{
				Name: "team-a", Namespaces: []string{defaultNS}, Ready: true,
				Message: "Counted 3 nodes and 2 pods", NodeCount: 3, ReadyNodes: 3, PodCount: 2,
			},
		}
		if got := decode[[]ClusterView](t, rec); !reflect.DeepEqual(got, want) {
			t.Errorf("body =\n%+v\nwant\n%+v", got, want)
		}
		if !strings.Contains(rec.Body.String(), `"namespaces":[]`) {
			t.Errorf("empty filter should encode as [], got %s", rec.Body.String())
		}
	})

	t.Run("list failure returns 500 without leaking the error", func(t *testing.T) {
		rec := get(t, failingServer(), http.MethodGet, "/api/clusterviews")
		if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "secret") {
			t.Errorf("got %d %s, want 500 without internal error", rec.Code, rec.Body.String())
		}
	})
}

func TestViewFilter(t *testing.T) {
	objects := func() []client.Object {
		return []client.Object{
			testNode("node-a", corev1.ConditionTrue),
			testPod(defaultNS, "web", "node-a", 0, true),
			testPod("kube-system", "dns", "node-a", 0, true),
			&observabilityv1alpha1.ClusterView{
				ObjectMeta: metav1.ObjectMeta{Name: "default-only"},
				Spec:       observabilityv1alpha1.ClusterViewSpec{Namespaces: []string{defaultNS}},
			},
			&observabilityv1alpha1.ClusterView{ObjectMeta: metav1.ObjectMeta{Name: allView}},
		}
	}

	podCount := func(t *testing.T, path string) int {
		t.Helper()
		rec := get(t, newServer(objects()...), http.MethodGet, path)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", path, rec.Code)
		}
		return decode[[]Node](t, rec)[0].PodCount
	}

	t.Run("node pod counts follow the view's namespaces", func(t *testing.T) {
		if got := podCount(t, "/api/nodes"); got != 2 {
			t.Errorf("no view: podCount = %d, want 2", got)
		}
		if got := podCount(t, "/api/nodes?view=all"); got != 2 {
			t.Errorf("view with empty filter: podCount = %d, want 2", got)
		}
		if got := podCount(t, "/api/nodes?view=default-only"); got != 1 {
			t.Errorf("default-only view: podCount = %d, want 1", got)
		}
	})

	t.Run("node pod list follows the view's namespaces", func(t *testing.T) {
		rec := get(t, newServer(objects()...), http.MethodGet, "/api/nodes/node-a/pods?view=default-only")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		pods := decode[[]Pod](t, rec)
		if len(pods) != 1 || pods[0].Name != "web" {
			t.Errorf("pods = %+v, want only web", pods)
		}
	})

	t.Run("unknown view returns 404 on both endpoints", func(t *testing.T) {
		for _, path := range []string{"/api/nodes?view=nope", "/api/nodes/node-a/pods?view=nope"} {
			rec := get(t, newServer(objects()...), http.MethodGet, path)
			if rec.Code != http.StatusNotFound {
				t.Errorf("GET %s status = %d, want 404", path, rec.Code)
			}
			if got := decode[map[string]string](t, rec); got["error"] != "clusterview not found" {
				t.Errorf("GET %s error = %q", path, got["error"])
			}
		}
	})
}
