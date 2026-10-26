/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	observabilityv1alpha1 "github.com/arjunmalhotra1/aranya/api/v1alpha1"
)

// envtest runs no kubelet or scheduler, so tests set node conditions, pod
// node names and pod phases directly.

func createNode(name string, ready corev1.ConditionStatus) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}}
	Expect(k8sClient.Create(ctx, node)).To(Succeed())
	DeferCleanup(func() { Expect(k8sClient.Delete(ctx, node)).To(Succeed()) })

	node.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: ready}}
	Expect(k8sClient.Status().Update(ctx, node)).To(Succeed())
}

func createPod(namespace, name, nodeName string, phase corev1.PodPhase) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec: corev1.PodSpec{
			NodeName:   nodeName,
			Containers: []corev1.Container{{Name: "app", Image: "busybox"}},
		},
	}
	Expect(k8sClient.Create(ctx, pod)).To(Succeed())
	// Grace period 0: with no kubelet, a bound pod would otherwise linger.
	DeferCleanup(func() { Expect(k8sClient.Delete(ctx, pod, client.GracePeriodSeconds(0))).To(Succeed()) })

	pod.Status.Phase = phase
	Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
}

func createClusterView(name string, spec observabilityv1alpha1.ClusterViewSpec) client.ObjectKey {
	cv := &observabilityv1alpha1.ClusterView{ObjectMeta: metav1.ObjectMeta{Name: name}, Spec: spec}
	Expect(k8sClient.Create(ctx, cv)).To(Succeed())
	DeferCleanup(func() { Expect(k8sClient.Delete(ctx, cv)).To(Succeed()) })
	return client.ObjectKeyFromObject(cv)
}

var _ = Describe("ClusterView Controller", func() {
	var reconciler *ClusterViewReconciler

	BeforeEach(func() {
		reconciler = &ClusterViewReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
	})

	It("summarizes nodes and pods into status", func() {
		createNode("node-ready", corev1.ConditionTrue)
		createNode("node-down", corev1.ConditionFalse)
		createPod("default", "web-1", "node-ready", corev1.PodRunning)
		createPod("default", "web-2", "node-ready", corev1.PodPending)
		createPod("kube-system", "dns", "node-down", corev1.PodRunning)
		createPod("default", "unscheduled", "", corev1.PodPending)
		key := createClusterView("all", observabilityv1alpha1.ClusterViewSpec{})

		result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(defaultRefreshInterval))

		var cv observabilityv1alpha1.ClusterView
		Expect(k8sClient.Get(ctx, key, &cv)).To(Succeed())
		Expect(cv.Status.NodeCount).To(Equal(int32(2)))
		Expect(cv.Status.ReadyNodes).To(Equal(int32(1)))
		Expect(cv.Status.NotReadyNodes).To(Equal(int32(1)))
		Expect(cv.Status.PodCount).To(Equal(int32(4)))
		Expect(cv.Status.PodPhases).To(Equal(observabilityv1alpha1.PodPhaseCounts{Pending: 2, Running: 2}))
		Expect(cv.Status.PodsPerNode).To(Equal([]observabilityv1alpha1.NodePodCount{
			{NodeName: "node-down", Pods: 1},
			{NodeName: "node-ready", Pods: 2},
		}))
		Expect(cv.Status.ObservedGeneration).To(Equal(cv.Generation))
		Expect(cv.Status.LastUpdated).NotTo(BeNil())
		Expect(meta.IsStatusConditionTrue(cv.Status.Conditions, observabilityv1alpha1.ConditionReady)).To(BeTrue())
		Expect(meta.IsStatusConditionFalse(cv.Status.Conditions, observabilityv1alpha1.ConditionDegraded)).To(BeTrue())
	})

	It("counts only pods in spec.namespaces but every node", func() {
		createNode("node-a", corev1.ConditionTrue)
		createNode("node-b", corev1.ConditionTrue)
		createPod("default", "web", "node-a", corev1.PodRunning)
		createPod("kube-system", "dns", "node-b", corev1.PodRunning)
		key := createClusterView("default-only", observabilityv1alpha1.ClusterViewSpec{
			Namespaces: []string{"default"},
		})

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())

		var cv observabilityv1alpha1.ClusterView
		Expect(k8sClient.Get(ctx, key, &cv)).To(Succeed())
		Expect(cv.Status.NodeCount).To(Equal(int32(2)))
		Expect(cv.Status.PodCount).To(Equal(int32(1)))
		Expect(cv.Status.PodsPerNode).To(Equal([]observabilityv1alpha1.NodePodCount{
			{NodeName: "node-a", Pods: 1},
			{NodeName: "node-b", Pods: 0},
		}))
	})

	It("does nothing for a ClusterView that no longer exists", func() {
		result, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: client.ObjectKey{Name: "deleted"},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(reconcile.Result{}))
	})

	It("writes status only when it changes", func() {
		key := createClusterView("skip", observabilityv1alpha1.ClusterViewSpec{})
		reconcileAndGet := func() observabilityv1alpha1.ClusterView {
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
			var cv observabilityv1alpha1.ClusterView
			Expect(k8sClient.Get(ctx, key, &cv)).To(Succeed())
			return cv
		}

		cv := reconcileAndGet()
		// lastUpdated is stored with one-second precision, so two reconciles in
		// the same second would write identical bytes even without the skip.
		// Backdating it makes any real write visible.
		hourAgo := metav1.NewTime(time.Now().Add(-time.Hour).Truncate(time.Second))
		cv.Status.LastUpdated = &hourAgo
		Expect(k8sClient.Status().Update(ctx, &cv)).To(Succeed())

		unchanged := reconcileAndGet()
		Expect(unchanged.ResourceVersion).To(Equal(cv.ResourceVersion), "unchanged cluster should not be rewritten")
		Expect(unchanged.Status.LastUpdated.Time).To(BeTemporally("==", hourAgo.Time))

		createNode("node-new", corev1.ConditionTrue)
		changed := reconcileAndGet()
		Expect(changed.ResourceVersion).NotTo(Equal(cv.ResourceVersion), "a new node should be written")
		Expect(changed.Status.LastUpdated.Time).To(BeTemporally(">", hourAgo.Time))
	})
})
