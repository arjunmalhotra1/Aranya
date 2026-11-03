package apiserver

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	observabilityv1alpha1 "github.com/arjunmalhotra1/aranya/api/v1alpha1"
)

func testDeployment(namespace, name string, desired, available int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec: appsv1.DeploymentSpec{
			Replicas: &desired,
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: appContainer, Image: name + ":v1"}},
			}},
		},
		Status: appsv1.DeploymentStatus{
			Replicas:          desired,
			UpdatedReplicas:   desired,
			ReadyReplicas:     available,
			AvailableReplicas: available,
		},
	}
}

func TestListDeployments(t *testing.T) {
	t.Run("returns deployments sorted, with replica counts and rollout state", func(t *testing.T) {
		s := newServer(
			testDeployment("kube-system", "coredns", 2, 2),
			testDeployment(defaultNS, webName, 3, 3),
			testDeployment(defaultNS, "api", 2, 1),
		)
		rec := get(t, s, http.MethodGet, "/api/deployments")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		want := []Deployment{
			{Namespace: defaultNS, Name: "api", Desired: 2, Ready: 1, UpToDate: 2, Available: 1, Images: []string{"api:v1"}},
			{Namespace: defaultNS, Name: webName, Desired: 3, Ready: 3, UpToDate: 3, Available: 3, RolledOut: true, Images: []string{"web:v1"}},
			{Namespace: "kube-system", Name: "coredns", Desired: 2, Ready: 2, UpToDate: 2, Available: 2, RolledOut: true, Images: []string{"coredns:v1"}},
		}
		if got := decode[[]Deployment](t, rec); !reflect.DeepEqual(got, want) {
			t.Errorf("body =\n%+v\nwant\n%+v", got, want)
		}
	})

	t.Run("?view= filters by the ClusterView's namespaces", func(t *testing.T) {
		view := &observabilityv1alpha1.ClusterView{
			ObjectMeta: metav1.ObjectMeta{Name: "default-only"},
			Spec:       observabilityv1alpha1.ClusterViewSpec{Namespaces: []string{defaultNS}},
		}
		s := newServer(view, testDeployment(defaultNS, webName, 1, 1), testDeployment("kube-system", "coredns", 1, 1))
		got := decode[[]Deployment](t, get(t, s, http.MethodGet, "/api/deployments?view=default-only"))
		if len(got) != 1 || got[0].Name != webName {
			t.Errorf("got %+v, want only default/web", got)
		}
	})

	t.Run("unknown view returns 404", func(t *testing.T) {
		if rec := get(t, newServer(), http.MethodGet, "/api/deployments?view=nope"); rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("no deployments returns an empty JSON array", func(t *testing.T) {
		rec := get(t, newServer(), http.MethodGet, "/api/deployments")
		if body := strings.TrimSpace(rec.Body.String()); rec.Code != http.StatusOK || body != "[]" {
			t.Errorf("got %d %s, want 200 []", rec.Code, body)
		}
	})

	t.Run("list failure returns 500 without leaking the error", func(t *testing.T) {
		rec := get(t, failingServer(), http.MethodGet, "/api/deployments")
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
		if strings.Contains(rec.Body.String(), "secret") {
			t.Errorf("body leaks internal error: %s", rec.Body.String())
		}
	})
}
